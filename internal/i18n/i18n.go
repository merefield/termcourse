package i18n

import (
	"embed"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

const DefaultLocale = "en"

var AvailableLocales = []string{"en", "fr", "de", "es", "nl", "it", "ru", "ja", "zh-Hans", "sv", "nb", "tr", "et", "fi", "pt-BR", "pt-PT", "da"}

var localeMatcher = func() language.Matcher {
	tags := make([]language.Tag, len(AvailableLocales))
	for i, code := range AvailableLocales {
		tags[i] = language.MustParse(code)
	}
	return language.NewMatcher(tags)
}()

//go:embed locales/*.yml
var localeFiles embed.FS

var (
	once         sync.Once
	translations map[string]map[string]string
)

// ResolveLocale follows termcourse's locale precedence and accepts values such
// as fr_FR.UTF-8 as well as BCP 47 tags and short language codes.
func ResolveLocale(value string) string {
	candidates := []string{value, os.Getenv("TERMCOURSE_LANG"), os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")}
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if len(raw) < 2 {
			continue
		}
		// POSIX locale names may add an encoding/modifier and use underscores.
		if end := strings.IndexAny(raw, ".@"); end >= 0 {
			raw = raw[:end]
		}
		raw = strings.ReplaceAll(raw, "_", "-")
		if tag, err := language.Parse(raw); err == nil {
			_, index, confidence := localeMatcher.Match(tag)
			if confidence != language.No {
				return AvailableLocales[index]
			}
		}
		return DefaultLocale
	}
	return DefaultLocale
}

// T looks up a dotted translation key, falling back to English and then the
// key itself. %{name} placeholders are replaced from vars; %{locales} is the
// supported locale list used by language help.
func T(locale, key string, vars map[string]any) string {
	once.Do(load)
	locale = ResolveLocale(locale)
	value := translations[locale][key]
	if value == "" {
		value = translations[DefaultLocale][key]
	}
	if value == "" {
		value = key
	}
	if strings.Contains(value, "%{locales}") {
		value = strings.ReplaceAll(value, "%{locales}", strings.Join(AvailableLocales, ", "))
	}
	for name, v := range vars {
		value = strings.ReplaceAll(value, "%{"+name+"}", toString(v))
	}
	return value
}

func Tr(locale, key string, pairs ...any) string {
	vars := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		vars[toString(pairs[i])] = pairs[i+1]
	}
	return T(locale, key, vars)
}

func load() {
	translations = make(map[string]map[string]string)
	for _, locale := range AvailableLocales {
		body, err := localeFiles.ReadFile("locales/" + locale + ".yml")
		if err != nil {
			continue
		}
		var document map[string]any
		if yaml.Unmarshal(body, &document) != nil {
			continue
		}
		translations[locale] = make(map[string]string)
		flatten(translations[locale], "", document[locale])
	}
}

func flatten(out map[string]string, prefix string, value any) {
	if values, ok := value.(map[string]any); ok {
		for key, nested := range values {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			flatten(out, path, nested)
		}
		return
	}
	if prefix != "" && value != nil {
		out[prefix] = toString(value)
	}
}

func toString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return fmt.Sprint(v)
	}
}

// Upper preserves established casing except for Turkish's dotted/dotless i.
// Call it on plain presentation text before terminal widths are measured.
func Upper(locale, value string) string {
	if ResolveLocale(locale) == "tr" {
		return cases.Upper(language.Turkish).String(value)
	}
	return strings.ToUpper(value)
}
