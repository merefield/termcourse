package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Credentials struct {
	Auth        string
	Username    string
	Password    string
	APIKey      string
	APIUsername string
}

// LoadDotEnv loads missing environment keys from .env in the working
// directory. Existing environment variables always win.
func LoadDotEnv(path string) {
	_ = godotenv.Load(path)
}

func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("missing URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("invalid URL")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func LoadSiteCredentials(baseURL string) Credentials {
	path := strings.TrimSpace(os.Getenv("TERMCOURSE_CREDENTIALS_FILE"))
	if path == "" {
		path = defaultFile("credentials.yml")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}
	}
	var file struct {
		Sites map[string]struct {
			Auth           string `yaml:"auth"`
			AuthEnv        string `yaml:"auth_env"`
			Username       string `yaml:"username"`
			UsernameEnv    string `yaml:"username_env"`
			Password       string `yaml:"password"`
			PasswordEnv    string `yaml:"password_env"`
			APIKey         string `yaml:"api_key"`
			APIKeyEnv      string `yaml:"api_key_env"`
			APIUsername    string `yaml:"api_username"`
			APIUsernameEnv string `yaml:"api_username_env"`
		} `yaml:"sites"`
	}
	if yaml.Unmarshal(body, &file) != nil {
		return Credentials{}
	}
	parsed, _ := url.Parse(baseURL)
	host := strings.ToLower(parsed.Hostname())
	site, exists := file.Sites[host]
	if !exists {
		site = file.Sites[strings.TrimPrefix(host, "www.")]
	}
	value := func(direct, envName string) string {
		if direct == "" && envName != "" {
			return os.Getenv(envName)
		}
		return direct
	}
	return Credentials{
		Auth: value(site.Auth, site.AuthEnv), Username: value(site.Username, site.UsernameEnv),
		Password: value(site.Password, site.PasswordEnv), APIKey: value(site.APIKey, site.APIKeyEnv),
		APIUsername: value(site.APIUsername, site.APIUsernameEnv),
	}
}

func defaultFile(name string) string {
	if info, err := os.Stat(name); err == nil && !info.IsDir() {
		absolute, _ := filepath.Abs(name)
		return absolute
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "termcourse", name)
}
