package i18n

import (
	"strings"
	"testing"
)

func TestResolveLocale(t *testing.T) {
	t.Setenv("TERMCOURSE_LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
	for input, expected := range map[string]string{
		"fr_FR.UTF-8": "fr", "de_DE.UTF-8": "de", "es_ES": "es", "it_IT": "en", "": "en",
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
