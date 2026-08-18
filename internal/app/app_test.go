package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/merefield/termcourse"
	"github.com/merefield/termcourse/internal/theme"
)

func TestParseArgs(t *testing.T) {
	options, err := parseArgs([]string{"--lang=fr", "--theme", "slate", "--theme-file", "colors.yml", "--api-key", "key", "meta.discourse.org"})
	if err != nil {
		t.Fatal(err)
	}
	if options.lang != "fr" || options.theme != "slate" || options.themeFile != "colors.yml" || options.apiKey != "key" || options.baseURL != "meta.discourse.org" {
		t.Fatalf("options = %#v", options)
	}
}

func TestParseArgsRejectsMissingOptionValues(t *testing.T) {
	for _, argv := range [][]string{
		{"--theme", "--lang", "fr", "meta.discourse.org"},
		{"--theme="},
	} {
		if _, err := parseArgs(argv); err == nil || !strings.Contains(err.Error(), "--theme requires a value") {
			t.Fatalf("parseArgs(%q) error = %v", argv, err)
		}
	}
}

func TestVersionFlagNeedsNoURLOrAuthentication(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if status := Run([]string{"--version"}, os.Stdin, &stdout, &stderr); status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr.String())
	}
	expected := "termcourse " + termcourse.CurrentVersion() + "\n"
	if stdout.String() != expected {
		t.Fatalf("version output = %q, want %q", stdout.String(), expected)
	}
}

func TestParseThemesCommandWithOptionalName(t *testing.T) {
	options, err := parseArgs([]string{"themes", "hacker"})
	if err != nil || options.command != "themes" || options.theme != "hacker" {
		t.Fatalf("options = %#v, %v", options, err)
	}
	if _, err := parseArgs([]string{"themes", "hacker", "extra"}); err == nil {
		t.Fatal("extra theme argument should fail")
	}
}

func TestThemeSelectionUsesCLIThenEnvironmentThenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.yml")
	body := "theme: rust\nthemes:\n  ocean:\n    primary: '#010203'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := theme.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERMCOURSE_THEME", "slate")
	selected, err := resolveTheme(catalog, cliOptions{theme: "hacker"})
	if err != nil || selected.Name != "hacker" {
		t.Fatalf("CLI theme = %#v, %v", selected, err)
	}
	selected, err = resolveTheme(catalog, cliOptions{})
	if err != nil || selected.Name != "slate" {
		t.Fatalf("environment theme = %#v, %v", selected, err)
	}
	t.Setenv("TERMCOURSE_THEME", "")
	selected, err = resolveTheme(catalog, cliOptions{})
	if err != nil || selected.Name != "rust" {
		t.Fatalf("file theme = %#v, %v", selected, err)
	}
}

func TestThemesCommandListsAllOrPreviewsOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.yml")
	if err := os.WriteFile(path, []byte("themes:\n  ocean:\n    primary: '#010203'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERMCOURSE_THEME", "slate")
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	for _, test := range []struct {
		argv   []string
		prefix string
	}{
		{[]string{"themes", "--theme-file", path}, "default ("},
		{[]string{"themes", "ocean", "--theme-file", path}, "ocean ("},
	} {
		var stdout, stderr bytes.Buffer
		if status := Run(test.argv, os.Stdin, &stdout, &stderr); status != 0 {
			t.Fatalf("status = %d, stderr = %q", status, stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), test.prefix) {
			t.Fatalf("preview = %q, want prefix %q", stdout.String(), test.prefix)
		}
	}
}

func TestMFARequiredDoesNotTreatFalseAsRequired(t *testing.T) {
	if mfaRequired(map[string]any{"second_factor_required": false}) {
		t.Fatal("false second_factor_required should not prompt")
	}
	if !mfaRequired(map[string]any{"requires_second_factor": true}) {
		t.Fatal("true requires_second_factor should prompt")
	}
}
