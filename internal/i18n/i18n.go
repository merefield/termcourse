package i18n

import (
	"embed"
	"fmt"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const DefaultLocale = "en"

var AvailableLocales = []string{"en", "fr", "de", "es"}

//go:embed locales/*.yml
var localeFiles embed.FS

var (
	once         sync.Once
	translations map[string]map[string]string
)

// ResolveLocale follows termcourse's locale precedence and accepts values such
// as fr_FR.UTF-8 as well as the short language code.
func ResolveLocale(value string) string {
	candidates := []string{value, os.Getenv("TERMCOURSE_LANG"), os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")}
	for _, raw := range candidates {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if len(raw) < 2 {
			continue
		}
		code := raw[:2]
		for _, locale := range AvailableLocales {
			if code == locale {
				return code
			}
		}
		return DefaultLocale
	}
	return DefaultLocale
}

// T looks up a dotted translation key, falling back to English and then the
// key itself. %{name} placeholders are replaced from vars.
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
