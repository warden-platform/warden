package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var placeholderPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Admin    AdminConfig    `yaml:"admin"`
	Web      WebConfig      `yaml:"web"`
	OIDC     OIDCConfig     `yaml:"oidc"`
	AI       AIConfig       `yaml:"ai"`
}

type ServerConfig struct {
	Port         int    `yaml:"port"`
	PublicURL    string `yaml:"public_url"`
	CORSOrigin   string `yaml:"cors_origin"`
	CookieSecure bool   `yaml:"cookie_secure"`
}

type DatabaseConfig struct {
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type AdminConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type WebConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	ControlTarget string `yaml:"control_target"`
}

type OIDCConfig struct {
	// Pointer so an absent yaml field stays distinct from an explicit false:
	// only an explicit value exports WARDEN_OIDC_ENABLED, preserving the legacy
	// "configured issuer enables SSO" contract for env-driven deployments.
	Enabled         *bool  `yaml:"enabled"`
	Issuer          string `yaml:"issuer"`
	ClientID        string `yaml:"client_id"`
	ClientSecret    string `yaml:"client_secret"`
	RedirectURL     string `yaml:"redirect_url"`
	DefaultTenantID string `yaml:"default_tenant_id"`
	DefaultRole     string `yaml:"default_role"`
}

type AIConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Model     string `yaml:"model"`
	BaseURL   string `yaml:"base_url"`
	Timeout   string `yaml:"timeout"`
	MaxTokens int    `yaml:"max_tokens"`
}

// LoadRuntime reads config.yaml (optional), .env and the generated internal
// secrets file, then exports the resulting values as WARDEN_* environment
// variables for the existing backend packages. Precedence: process environment
// > .env > config.yaml > built-in defaults. Without config.yaml the runtime is
// purely environment-driven (the legacy production contract).
func LoadRuntime() (*Config, error) {
	cfg := defaultConfig()
	configPath := firstExisting("config.yaml", "../config.yaml")
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read config.yaml: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config.yaml: %w", err)
		}
	}

	dotenv, err := readDotEnv(firstExisting(".env", "../.env"))
	if err != nil {
		return nil, err
	}
	secrets, err := readInternalSecrets(firstExisting("var/warden/secrets.json", "../var/warden/secrets.json"))
	if err != nil {
		return nil, err
	}

	resolve := func(value string) string {
		return placeholderPattern.ReplaceAllStringFunc(value, func(match string) string {
			name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
			if v, ok := os.LookupEnv(name); ok && v != "" {
				return v
			}
			if v, ok := dotenv[name]; ok {
				return v
			}
			if v, ok := secrets[name]; ok {
				return v
			}
			return ""
		})
	}

	cfg.Admin.Password = resolve(cfg.Admin.Password)
	cfg.Database.Password = resolve(cfg.Database.Password)
	cfg.OIDC.ClientSecret = resolve(cfg.OIDC.ClientSecret)
	cfg.AI.BaseURL = resolve(cfg.AI.BaseURL)

	// Files holding explicit values win over config.yaml defaults; the process
	// environment (already set) wins over both via setIfAbsent.
	for key, value := range secrets {
		setIfAbsent(key, value)
	}
	for key, value := range dotenv {
		setIfAbsent(key, value)
	}
	if configPath != "" {
		applyRuntimeEnvironment(cfg)
	}
	overlayProcessEnvironment(cfg)
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{Port: 8080, CookieSecure: false},
		Web:    WebConfig{Host: "localhost", Port: 4173, ControlTarget: "http://localhost:8080"},
	}
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func readDotEnv(path string) (map[string]string, error) {
	values := map[string]string{}
	if path == "" {
		return values, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, fmt.Errorf("read .env: %w", err)
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid .env line: %q", raw)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			return nil, fmt.Errorf("empty .env key")
		}
		values[key] = value
	}
	return values, nil
}

func readInternalSecrets(path string) (map[string]string, error) {
	values := map[string]string{}
	if path == "" {
		return values, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, fmt.Errorf("read internal secrets: %w", err)
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("parse internal secrets: %w", err)
	}
	return values, nil
}

func applyRuntimeEnvironment(cfg *Config) {
	setIfAbsent("WARDEN_PORT", strconv.Itoa(cfg.Server.Port))
	setIfAbsent("WARDEN_PUBLIC_CONTROL_URL", cfg.Server.PublicURL)
	setIfAbsent("WARDEN_CORS_ORIGIN", cfg.Server.CORSOrigin)
	setIfAbsent("WARDEN_COOKIE_SECURE", strconv.FormatBool(cfg.Server.CookieSecure))

	setIfAbsent("WARDEN_DATABASE_URL", buildDatabaseDSN(cfg.Database))
	setIfAbsent("WARDEN_ADMIN_USERNAME", cfg.Admin.Username)
	setIfAbsent("WARDEN_ADMIN_PASSWORD", cfg.Admin.Password)

	setIfAbsent("WARDEN_WEB_HOST", cfg.Web.Host)
	setIfAbsent("WARDEN_WEB_PORT", strconv.Itoa(cfg.Web.Port))
	setIfAbsent("WARDEN_CONTROL_TARGET", cfg.Web.ControlTarget)

	if cfg.OIDC.Enabled != nil {
		setIfAbsent("WARDEN_OIDC_ENABLED", strconv.FormatBool(*cfg.OIDC.Enabled))
	}
	setIfAbsent("WARDEN_OIDC_ISSUER", cfg.OIDC.Issuer)
	setIfAbsent("WARDEN_OIDC_CLIENT_ID", cfg.OIDC.ClientID)
	setIfAbsent("WARDEN_OIDC_CLIENT_SECRET", cfg.OIDC.ClientSecret)
	setIfAbsent("WARDEN_OIDC_REDIRECT_URL", cfg.OIDC.RedirectURL)
	setIfAbsent("WARDEN_OIDC_DEFAULT_TENANT_ID", cfg.OIDC.DefaultTenantID)
	setIfAbsent("WARDEN_OIDC_DEFAULT_ROLE", cfg.OIDC.DefaultRole)

	setIfAbsent("WARDEN_AI_ENABLED", strconv.FormatBool(cfg.AI.Enabled))
	setIfAbsent("WARDEN_AI_MODEL", cfg.AI.Model)
	setIfAbsent("WARDEN_AI_BASE_URL", cfg.AI.BaseURL)
	setIfAbsent("WARDEN_AI_TIMEOUT", cfg.AI.Timeout)
	if cfg.AI.MaxTokens > 0 {
		setIfAbsent("WARDEN_AI_MAX_TOKENS", strconv.Itoa(cfg.AI.MaxTokens))
	}
}

func overlayProcessEnvironment(cfg *Config) {
	if v := os.Getenv("WARDEN_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("WARDEN_PUBLIC_CONTROL_URL"); v != "" {
		cfg.Server.PublicURL = v
	}
	if v := os.Getenv("WARDEN_CORS_ORIGIN"); v != "" {
		cfg.Server.CORSOrigin = v
	}
	if v := os.Getenv("WARDEN_COOKIE_SECURE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Server.CookieSecure = b
		}
	}
	if v := os.Getenv("WARDEN_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
		cfg.Database.Username = ""
		cfg.Database.Password = ""
	}
	if v := os.Getenv("WARDEN_ADMIN_USERNAME"); v != "" {
		cfg.Admin.Username = v
	}
	if v := os.Getenv("WARDEN_ADMIN_PASSWORD"); v != "" {
		cfg.Admin.Password = v
	}
	if v := os.Getenv("WARDEN_WEB_HOST"); v != "" {
		cfg.Web.Host = v
	}
	if v := os.Getenv("WARDEN_WEB_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Web.Port = port
		}
	}
	if v := os.Getenv("WARDEN_CONTROL_TARGET"); v != "" {
		cfg.Web.ControlTarget = v
	}
	if v := os.Getenv("WARDEN_OIDC_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.OIDC.Enabled = &b
		}
	}
	if v := os.Getenv("WARDEN_OIDC_ISSUER"); v != "" {
		cfg.OIDC.Issuer = v
	}
	if v := os.Getenv("WARDEN_OIDC_CLIENT_ID"); v != "" {
		cfg.OIDC.ClientID = v
	}
	if v := os.Getenv("WARDEN_OIDC_CLIENT_SECRET"); v != "" {
		cfg.OIDC.ClientSecret = v
	}
	if v := os.Getenv("WARDEN_OIDC_REDIRECT_URL"); v != "" {
		cfg.OIDC.RedirectURL = v
	}
	if v := os.Getenv("WARDEN_OIDC_DEFAULT_TENANT_ID"); v != "" {
		cfg.OIDC.DefaultTenantID = v
	}
	if v := os.Getenv("WARDEN_OIDC_DEFAULT_ROLE"); v != "" {
		cfg.OIDC.DefaultRole = v
	}
	if v := os.Getenv("WARDEN_AI_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AI.Enabled = b
		}
	}
	if v := os.Getenv("WARDEN_AI_MODEL"); v != "" {
		cfg.AI.Model = v
	}
	if v := os.Getenv("WARDEN_AI_BASE_URL"); v != "" {
		cfg.AI.BaseURL = v
	}
	if v := os.Getenv("WARDEN_AI_TIMEOUT"); v != "" {
		cfg.AI.Timeout = v
	}
	if v := os.Getenv("WARDEN_AI_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.AI.MaxTokens = n
		}
	}
}

func buildDatabaseDSN(db DatabaseConfig) string {
	u, err := url.Parse(db.URL)
	if err != nil {
		return db.URL
	}
	if db.Username != "" {
		if db.Password != "" {
			u.User = url.UserPassword(db.Username, db.Password)
		} else {
			u.User = url.User(db.Username)
		}
	}
	return u.String()
}

func setIfAbsent(key, value string) {
	if value == "" {
		return
	}
	if v, exists := os.LookupEnv(key); exists && v != "" {
		return
	}
	_ = os.Setenv(key, value)
}
