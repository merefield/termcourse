package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	actual, err := NormalizeBaseURL("meta.discourse.org/some/path")
	if err != nil || actual != "https://meta.discourse.org" {
		t.Fatalf("NormalizeBaseURL = %q, %v", actual, err)
	}
	if _, err := NormalizeBaseURL(""); err == nil {
		t.Fatal("empty URL should fail")
	}
}

func TestLoadDotEnvUsesStandardSyntaxWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "export TERMCOURSE_DOTENV_QUOTED=\"value with spaces\"\nTERMCOURSE_DOTENV_KEEP=file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERMCOURSE_DOTENV_KEEP", "environment")
	LoadDotEnv(path)
	if actual := os.Getenv("TERMCOURSE_DOTENV_QUOTED"); actual != "value with spaces" {
		t.Fatalf("quoted dotenv value = %q", actual)
	}
	if actual := os.Getenv("TERMCOURSE_DOTENV_KEEP"); actual != "environment" {
		t.Fatalf("existing environment value = %q", actual)
	}
}

func TestSiteCredentialsAndEnvIndirection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yml")
	body := "sites:\n  meta.discourse.org:\n    auth: api\n    api_username: system\n    api_key_env: TEST_SITE_KEY\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERMCOURSE_CREDENTIALS_FILE", path)
	t.Setenv("TEST_SITE_KEY", "secret")
	credentials := LoadSiteCredentials("https://meta.discourse.org")
	if credentials.Auth != "api" || credentials.APIUsername != "system" || credentials.APIKey != "secret" {
		t.Fatalf("credentials = %#v", credentials)
	}
}
