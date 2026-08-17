package theme

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Color string

type Theme struct {
	Name             string
	Primary          Color
	Background       Color
	Selected         Color
	SelectedText     Color
	Border           Color
	HeaderBackground Color
	Separator        Color
	ListNumber       Color
	ListText         Color
	PostUsername     Color
	ListMeta         Color
	Accent           Color
}

type Catalog struct {
	themes   map[string]Theme
	selected string
}

type override struct {
	Primary         *string `yaml:"primary"`
	Background      *string `yaml:"background"`
	Highlighted     *string `yaml:"highlighted"`
	HighlightedText *string `yaml:"highlighted_text"`
	Borders         *string `yaml:"borders"`
	BarBackgrounds  *string `yaml:"bar_backgrounds"`
	Separators      *string `yaml:"separators"`
	ListNumbers     *string `yaml:"list_numbers"`
	ListText        *string `yaml:"list_text"`
	PostUsername    *string `yaml:"post_username"`
	ListMeta        *string `yaml:"list_meta"`
	Accent          *string `yaml:"accent"`
}

type themeFile struct {
	Theme  string              `yaml:"theme"`
	Themes map[string]override `yaml:"themes"`
}

var hexColor = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)

var builtinOrder = []string{"default", "slate", "fairground", "rust", "hacker"}

var builtins = map[string]Theme{
	"default": {
		Name: "default", Primary: "#f2f2f2", Selected: "#2a5ea8", SelectedText: "#ffffff",
		Border: "#a8a8a8", HeaderBackground: "#1f1f1f", Separator: "#6f6f6f",
		ListNumber: "#f2f2f2", ListText: "#e6e6e6", PostUsername: "#b5b5b5",
		ListMeta: "#b5b5b5", Accent: "#6cc4ff",
	},
	"slate": {
		Name: "slate", Primary: "#e6edf3", Selected: "#355f8a", SelectedText: "#ffffff",
		Border: "#5f6f80", HeaderBackground: "#1f2733", Separator: "#8ca0b3",
		ListNumber: "#8fbce6", ListText: "#dde7f0", PostUsername: "#9ab0c6",
		ListMeta: "#9ab0c6", Accent: "#66c2ff",
	},
	"fairground": {
		Name: "fairground", Primary: "#f6fff5", Selected: "#0055aa", SelectedText: "#ffffff",
		Border: "#1f8f5f", HeaderBackground: "#103f33", Separator: "#ff4b4b",
		ListNumber: "#4ecb71", ListText: "#d8f7e4", PostUsername: "#8fd9ad",
		ListMeta: "#8fd9ad", Accent: "#2a9dff",
	},
	"rust": {
		Name: "rust", Primary: "#efe2c4", Selected: "#b5521e", SelectedText: "#fff7e8",
		Border: "#b06c2f", HeaderBackground: "#3a2516", Separator: "#d2b168",
		ListNumber: "#d58a3d", ListText: "#f2e4c8", PostUsername: "#c9b287",
		ListMeta: "#c9b287", Accent: "#e0b85f",
	},
	"hacker": {
		Name: "hacker", Primary: "#2cfc03", Selected: "#1a9402", SelectedText: "#000000",
		Border: "#23c602", HeaderBackground: "#0a1f02", Separator: "#145c01",
		ListNumber: "#2cfc03", ListText: "#1df002", PostUsername: "#158a01",
		ListMeta: "#158a01", Accent: "#8cff00",
	},
}

func Load(filePath string) (Catalog, error) {
	catalog := Catalog{themes: make(map[string]Theme, len(builtins))}
	for name, value := range builtins {
		catalog.themes[name] = value
	}

	explicit := strings.TrimSpace(filePath) != ""
	if !explicit {
		filePath = DefaultFile()
	}
	body, err := os.ReadFile(filePath)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return catalog, nil
		}
		return Catalog{}, fmt.Errorf("read theme file %q: %w", filePath, err)
	}

	selected, overrides, err := decodeFile(body)
	if err != nil {
		return Catalog{}, fmt.Errorf("parse theme file %q: %w", filePath, err)
	}
	catalog.selected = normalizeName(selected)
	for rawName, values := range overrides {
		name := normalizeName(rawName)
		if name == "" {
			return Catalog{}, fmt.Errorf("theme file %q contains an empty theme name", filePath)
		}
		base, exists := catalog.themes[name]
		if !exists {
			base = catalog.themes["default"]
		}
		base.Name = name
		if err := applyOverride(&base, values); err != nil {
			return Catalog{}, fmt.Errorf("theme %q: %w", name, err)
		}
		catalog.themes[name] = base
	}
	if catalog.selected != "" {
		if _, exists := catalog.themes[catalog.selected]; !exists {
			return Catalog{}, fmt.Errorf("theme file %q selects unknown theme %q (available: %s)", filePath, catalog.selected, strings.Join(catalog.Names(), ", "))
		}
	}
	return catalog, nil
}

func decodeFile(body []byte) (string, map[string]override, error) {
	var root map[string]yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return "", nil, err
	}
	_, hasTheme := root["theme"]
	_, hasThemes := root["themes"]
	if hasTheme || hasThemes {
		var file themeFile
		if err := decodeKnown(body, &file); err != nil {
			return "", nil, err
		}
		return file.Theme, file.Themes, nil
	}
	var themes map[string]override
	if err := decodeKnown(body, &themes); err != nil {
		return "", nil, err
	}
	return "", themes, nil
}

func decodeKnown(body []byte, target any) error {
	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

func applyOverride(target *Theme, values override) error {
	fields := []struct {
		name  string
		value *string
		set   func(Color)
	}{
		{"primary", values.Primary, func(value Color) { target.Primary = value }},
		{"background", values.Background, func(value Color) { target.Background = value }},
		{"highlighted", values.Highlighted, func(value Color) { target.Selected = value }},
		{"highlighted_text", values.HighlightedText, func(value Color) { target.SelectedText = value }},
		{"borders", values.Borders, func(value Color) { target.Border = value }},
		{"bar_backgrounds", values.BarBackgrounds, func(value Color) { target.HeaderBackground = value }},
		{"separators", values.Separators, func(value Color) { target.Separator = value }},
		{"list_numbers", values.ListNumbers, func(value Color) { target.ListNumber = value }},
		{"list_text", values.ListText, func(value Color) { target.ListText = value }},
		{"post_username", values.PostUsername, func(value Color) { target.PostUsername = value }},
		{"list_meta", values.ListMeta, func(value Color) { target.ListMeta = value }},
		{"accent", values.Accent, func(value Color) { target.Accent = value }},
	}
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		color, err := ParseColor(*field.value)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
		field.set(color)
	}
	return nil
}

func ParseColor(value string) (Color, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "none" {
		return "", nil
	}
	named := map[string]string{
		"black": "#000000", "white": "#ffffff", "red": "#ff4b4b", "green": "#4ecb71",
		"blue": "#4a90e2", "yellow": "#ffd166", "cyan": "#66d9ef", "magenta": "#d38cff",
		"gray": "#9aa0a6", "grey": "#9aa0a6",
	}
	if replacement := named[value]; replacement != "" {
		return Color(replacement), nil
	}
	if hexColor.MatchString(value) {
		return Color("#" + strings.TrimPrefix(value, "#")), nil
	}
	if index, err := strconv.Atoi(value); err == nil && index >= 0 && index <= 255 {
		return Color(strconv.Itoa(index)), nil
	}
	return "", fmt.Errorf("invalid color %q (use #rrggbb, 0-255, a named color, or none)", value)
}

func (c Catalog) Resolve(requested string) (Theme, error) {
	name := normalizeName(requested)
	if name == "" {
		name = c.selected
	}
	if name == "" {
		name = "default"
	}
	value, exists := c.themes[name]
	if !exists {
		return Theme{}, fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(c.Names(), ", "))
	}
	return value, nil
}

func (c Catalog) Names() []string {
	result := make([]string, 0, len(c.themes))
	seen := make(map[string]bool, len(builtinOrder))
	for _, name := range builtinOrder {
		if _, exists := c.themes[name]; exists {
			result = append(result, name)
			seen[name] = true
		}
	}
	var custom []string
	for name := range c.themes {
		if !seen[name] {
			custom = append(custom, name)
		}
	}
	sort.Strings(custom)
	return append(result, custom...)
}

func (c Catalog) All() []Theme {
	result := make([]Theme, 0, len(c.themes))
	for _, name := range c.Names() {
		result = append(result, c.themes[name])
	}
	return result
}

func DefaultFile() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		directory = filepath.Join(home, ".config")
	}
	return filepath.Join(directory, "termcourse", "theme.yml")
}

func normalizeName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
