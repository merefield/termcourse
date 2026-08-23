package theme

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuiltinsIncludeHackerAndRejectUnknownNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	catalog, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	value, err := catalog.Resolve("hacker")
	if err != nil || value.Primary != "#2cfc03" || value.HeaderBackground != "#0a1f02" {
		t.Fatalf("hacker theme = %#v, %v", value, err)
	}
	if _, err := catalog.Resolve("missing"); err == nil || !strings.Contains(err.Error(), "available: default, slate, fairground, rust, hacker") {
		t.Fatalf("unknown theme error = %v", err)
	}
}

func TestWrappedThemeSelectionAndPartialOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.yml")
	body := "theme: ocean\nthemes:\n  ocean:\n    primary: '#010203'\n    accent: cyan\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := catalog.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if value.Name != "ocean" || value.Primary != "#010203" || value.Accent != "#66d9ef" || value.Border == "" {
		t.Fatalf("custom theme = %#v", value)
	}
}

func TestLegacyDirectThemeMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.yml")
	if err := os.WriteFile(path, []byte("slate:\n  primary: '#010203'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := catalog.Resolve("slate")
	if err != nil || value.Primary != "#010203" || value.Accent != "#66c2ff" {
		t.Fatalf("slate theme = %#v, %v", value, err)
	}
}

func TestDefaultThemeFileUsesUserConfigNotWorkingDirectory(t *testing.T) {
	workingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDirectory, "theme.yml"), []byte("theme: hacker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configRoot := t.TempDir()
	configDirectory := configRoot
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", configRoot)
		configDirectory = filepath.Join(configRoot, "Library", "Application Support")
	case "windows":
		t.Setenv("AppData", configRoot)
	default:
		t.Setenv("XDG_CONFIG_HOME", configRoot)
	}
	termcourseDirectory := filepath.Join(configDirectory, "termcourse")
	if err := os.MkdirAll(termcourseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(termcourseDirectory, "theme.yml"), []byte("theme: rust\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workingDirectory)
	catalog, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	value, err := catalog.Resolve("")
	if err != nil || value.Name != "rust" {
		t.Fatalf("selected theme = %#v, %v", value, err)
	}
}

func TestInvalidColorsAndFieldsAreActionable(t *testing.T) {
	for name, body := range map[string]string{
		"color": "themes:\n  bad:\n    primary: chartreuse\n",
		"field": "themes:\n  bad:\n    primarry: '#ffffff'\n",
		"name":  "theme: missing\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "theme.yml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("invalid theme should fail")
			}
		})
	}
}

func TestParseColorSupportsNoneHexIndexesAndNames(t *testing.T) {
	for input, expected := range map[string]Color{"none": "", "#AABBCC": "#aabbcc", "42": "42", "red": "#ff4b4b"} {
		actual, err := ParseColor(input)
		if err != nil || actual != expected {
			t.Fatalf("ParseColor(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
	if _, err := ParseColor("999"); err == nil {
		t.Fatal("out-of-range color index should fail")
	}
}
