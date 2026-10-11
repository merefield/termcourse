package i18n

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func TestResolveLocale(t *testing.T) {
	t.Setenv("TERMCOURSE_LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
	for input, expected := range map[string]string{
		"fr_FR.UTF-8": "fr", "de_DE.UTF-8": "de", "es_ES": "es", "it_IT": "it", "": "en",
	} {
		if actual := ResolveLocale(input); actual != expected {
			t.Fatalf("ResolveLocale(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestTranslationsAndInterpolation(t *testing.T) {
	if actual := Tr("fr", "ui.topic_list.filters.private"); actual != "Messages privés" {
		t.Fatalf("French private label = %q", actual)
	}
	actual := Tr("en", "ui.status.new_updated", "count", 7)
	if actual != "New/updated (7)" {
		t.Fatalf("interpolation = %q", actual)
	}
	if fallback := Tr("fr", "not.a.real.key"); !strings.Contains(fallback, "not.a.real.key") {
		t.Fatalf("missing-key fallback = %q", fallback)
	}
}

func clearLocaleEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"TERMCOURSE_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(key, "")
	}
}

func TestRegionalLocaleMatching(t *testing.T) {
	clearLocaleEnvironment(t)
	for input, want := range map[string]string{
		"en-GB": "en", "en_US.UTF-8": "en", "fr-CA": "fr", "de-AT": "de", "es-MX": "es",
		"nl-NL": "nl", "it_IT.UTF-8": "it", "ru_RU.UTF-8": "ru", "ja-JP": "ja",
		"zh-CN": "zh-Hans", "zh_Hans_CN.UTF-8": "zh-Hans", "zh-SG": "zh-Hans",
		"sv-FI": "sv", "nb-NO": "nb", "no": "nb", "no_NO.UTF-8": "nb",
		"tr_TR.UTF-8": "tr", "et-EE": "et", "fi_FI.UTF-8": "fi", "da-DK": "da", "da-GL": "da",
		"pt": "pt-BR", "pt-BR": "pt-BR", "pt_PT.UTF-8": "pt-PT", "pt-AO": "pt-PT", "pt-MZ": "pt-PT",
		"  PT_pt.UTF-8  ": "pt-PT", "de_DE.UTF-8@euro": "de",
		"": "en", "zz": "en", "not a language": "en", "POSIX": "en", "C.UTF-8": "en",
	} {
		if got := ResolveLocale(input); got != want {
			t.Errorf("%q: got %q, want %q", input, got, want)
		}
	}
	for _, code := range AvailableLocales {
		if got := ResolveLocale(code); got != code {
			t.Errorf("canonical locale %q matched %q", code, got)
		}
	}
}

func TestLocalePrecedence(t *testing.T) {
	clearLocaleEnvironment(t)
	t.Setenv("TERMCOURSE_LANG", "pt-PT")
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	t.Setenv("LC_MESSAGES", "tr_TR.UTF-8")
	t.Setenv("LANG", "fi_FI.UTF-8")
	if ResolveLocale("nl-NL") != "nl" {
		t.Fatal("CLI locale did not win")
	}
	if ResolveLocale("") != "pt-PT" {
		t.Fatal("TERMCOURSE_LANG did not win")
	}
	t.Setenv("TERMCOURSE_LANG", "")
	if ResolveLocale("") != "ja" {
		t.Fatal("LC_ALL did not win")
	}
	t.Setenv("LC_ALL", "")
	if ResolveLocale("") != "tr" {
		t.Fatal("LC_MESSAGES did not win")
	}
	t.Setenv("LC_MESSAGES", "")
	if ResolveLocale("") != "fi" {
		t.Fatal("LANG did not win")
	}
	if ResolveLocale("unsupported") != "en" {
		t.Fatal("unsupported explicit locale changed the fallback")
	}
}

func TestAllCataloguesAreCompleteAndPreserveVariablesAndShortcuts(t *testing.T) {
	clearLocaleEnvironment(t)
	read := func(code string) map[string]string {
		t.Helper()
		body, err := localeFiles.ReadFile("locales/" + code + ".yml")
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc) != 1 || doc[code] == nil {
			t.Fatalf("wrong catalogue root for %s", code)
		}
		entries := map[string]string{}
		flatten(entries, "", doc[code])
		return entries
	}
	english := read("en")
	variables := regexp.MustCompile(`%\{[^}]+\}`)
	hotkeys := regexp.MustCompile(`(?i)(?:ctrl\+d|x/esc|enter/1–9/0|(?:^|[ |])(?:t|f|g|q|l|r|p|x|esc|enter|↵):)`)
	sorted := func(pattern *regexp.Regexp, value string) []string {
		matches := pattern.FindAllString(strings.ReplaceAll(strings.ToLower(value), "entrée", "enter"), -1)
		for i := range matches {
			matches[i] = strings.TrimSpace(matches[i])
		}
		slices.Sort(matches)
		return matches
	}
	files, err := localeFiles.ReadDir("locales")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(AvailableLocales) {
		t.Fatal("embedded locales do not match the supported list")
	}
	for _, code := range AvailableLocales {
		t.Run(code, func(t *testing.T) {
			entries := read(code)
			if len(entries) != len(english) {
				t.Fatalf("%d entries, want %d", len(entries), len(english))
			}
			for key, source := range english {
				value, exists := entries[key]
				if !exists || strings.TrimSpace(value) == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\x1b\x00\r\n") {
					t.Errorf("invalid or missing entry %q", key)
					continue
				}
				if !slices.Equal(sorted(variables, source), sorted(variables, value)) {
					t.Errorf("changed placeholders in %s", key)
				}
				if strings.HasPrefix(key, "ui.controls.") || strings.HasPrefix(key, "ui.guidance.") || key == "ui.composer.reply_to_post" {
					if !slices.Equal(sorted(hotkeys, source), sorted(hotkeys, value)) {
						t.Errorf("changed keyboard shortcuts in %s", key)
					}
				}
				if !strings.Contains(value, "%{") && Tr(code, key) != value {
					t.Errorf("lookup used a fallback for %s", key)
				}
			}
		})
	}
}

func TestNewLocalesTranslateNavigationAndPreserveInterpolation(t *testing.T) {
	clearLocaleEnvironment(t)
	for code, want := range map[string]string{
		"nl": "Zoeken", "it": "Cerca", "ru": "Поиск", "ja": "検索", "zh-Hans": "搜索",
		"sv": "Sök", "nb": "Søk", "tr": "Ara", "et": "Otsi", "fi": "Haku",
		"pt-BR": "Buscar", "pt-PT": "Pesquisar", "da": "Søg",
	} {
		if got := Tr(code, "ui.tabs.search"); got != want {
			t.Errorf("%s: got %q, want %q", code, got, want)
		}
		if value := Tr(code, "ui.composer.reply_to_post", "post_number", 42); !strings.Contains(value, "#42") || strings.Contains(value, "%{") {
			t.Errorf("%s: interpolation failed: %q", code, value)
		}
		for _, key := range []string{"cli.help.lang", "cli.env.lang"} {
			value := Tr(code, key)
			for _, locale := range AvailableLocales {
				if !strings.Contains(value, locale) {
					t.Errorf("%s: %s hides %s", code, key, locale)
				}
			}
			if strings.Contains(value, "%{") {
				t.Errorf("language help not interpolated: %q", value)
			}
		}
	}
	if Tr("pt-BR", "cli.auth.password") != "Senha:" || Tr("pt-PT", "cli.auth.password") != "Palavra-passe:" {
		t.Fatal("Portuguese variants share the same wording")
	}
}

func TestTurkishPresentationCasing(t *testing.T) {
	clearLocaleEnvironment(t)
	if got := Upper("tr-TR", "bildirimler · çıkış"); got != "BİLDİRİMLER · ÇIKIŞ" {
		t.Fatal("incorrect Turkish casing", got)
	}
	if got := Upper("en", "Theme // test"); got != "THEME // TEST" {
		t.Fatal("English casing changed", got)
	}
}
