package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeResolvesFilesAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("config.yaml", `server:
  port: 8090
  public_url: http://localhost:8090
  cors_origin: http://localhost:4173
  cookie_secure: false
database:
  url: postgres://localhost:5432/pam
  username: pam
  password: ${WARDEN_DATABASE_PASSWORD}
admin:
  username: admin
  password: ${WARDEN_ADMIN_PASSWORD}
web:
  host: localhost
  port: 4173
  control_target: http://localhost:8090
oidc:
  enabled: false
ai:
  enabled: true
  model: test-model
  base_url: https://api.example.com/v1
  timeout: 30s
  max_tokens: 1024
`)
	writeFile(".env", `WARDEN_ADMIN_PASSWORD=admin-secret
WARDEN_DATABASE_PASSWORD=database-secret
`)
	writeFile(filepath.Join("var", "warden", "secrets.json"), `{
  "WARDEN_TASK_SIGNING_KEY": "task-secret",
  "WARDEN_JWT_SIGNING_KEY": "jwt-secret",
  "WARDEN_CREDENTIAL_ENCRYPTION_KEY": "credential-secret",
  "WARDEN_ADMIN_API_KEY": "api-secret"
}
`)

	for _, key := range []string{"WARDEN_PORT", "WARDEN_ADMIN_USERNAME", "WARDEN_ADMIN_PASSWORD", "WARDEN_DATABASE_URL", "WARDEN_TASK_SIGNING_KEY"} {
		t.Setenv(key, "")
	}

	cfg, err := LoadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8090 || cfg.Admin.Username != "admin" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Admin.Password != "admin-secret" {
		t.Fatalf("admin password = %q", cfg.Admin.Password)
	}
	if got := os.Getenv("WARDEN_DATABASE_URL"); got != "postgres://pam:database-secret@localhost:5432/pam" {
		t.Fatalf("database URL = %q", got)
	}
	if got := os.Getenv("WARDEN_TASK_SIGNING_KEY"); got != "task-secret" {
		t.Fatalf("task signing key = %q", got)
	}

	t.Setenv("WARDEN_PORT", "9090")
	cfg, err = LoadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9090 {
		t.Fatalf("process override not applied: %d", cfg.Server.Port)
	}
}

func TestLoadRuntimeWithoutConfigFileUsesEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("WARDEN_PORT", "9095")
	t.Setenv("WARDEN_DATABASE_URL", "postgres://pam:secret@localhost:5432/pam")
	t.Setenv("WARDEN_OIDC_ENABLED", "")
	t.Setenv("WARDEN_OIDC_ISSUER", "https://idp.example.com")

	cfg, err := LoadRuntime()
	if err != nil {
		t.Fatalf("env-only mode must not require config.yaml: %v", err)
	}
	if cfg.Server.Port != 9095 {
		t.Fatalf("port = %d", cfg.Server.Port)
	}
	if cfg.Database.URL != "postgres://pam:secret@localhost:5432/pam" {
		t.Fatalf("database URL = %q", cfg.Database.URL)
	}
	// Legacy contract: a configured issuer without an explicit switch must not
	// be force-disabled by an injected WARDEN_OIDC_ENABLED=false.
	if got := os.Getenv("WARDEN_OIDC_ENABLED"); got != "" {
		t.Fatalf("WARDEN_OIDC_ENABLED injected in env-only mode: %q", got)
	}
}

func TestDotEnvOverridesConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("WARDEN_PORT", "")
	if err := os.WriteFile("config.yaml", []byte("server:\n  port: 8090\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("WARDEN_PORT=9091\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9091 {
		t.Fatalf(".env must override config.yaml: port = %d", cfg.Server.Port)
	}
}

func TestAbsentOIDCSectionKeepsLegacyContract(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("WARDEN_OIDC_ENABLED", "")
	t.Setenv("WARDEN_OIDC_ISSUER", "https://idp.example.com")
	if err := os.WriteFile("config.yaml", []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntime(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("WARDEN_OIDC_ENABLED"); got != "" {
		t.Fatalf("absent oidc section must not inject a switch: %q", got)
	}
}
