package ai

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigAllowsUnconfiguredService(t *testing.T) {
	for _, name := range []string{"WARDEN_AI_ENABLED", "WARDEN_AI_MODEL", "WARDEN_AI_BASE_URL", "WARDEN_AI_API_KEY", "WARDEN_AI_API_KEY_FILE", "WARDEN_AI_TIMEOUT", "WARDEN_AI_MAX_TOKENS"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Configured() {
		t.Fatal("empty model configuration must not be marked configured")
	}
	if cfg.Timeout != 90*time.Second || cfg.MaxIterations != 8 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfigRejectsPartialModelConfiguration(t *testing.T) {
	t.Setenv("WARDEN_AI_MODEL", "test-model")
	t.Setenv("WARDEN_AI_API_KEY", "")
	t.Setenv("WARDEN_AI_API_KEY_FILE", "")
	_ = os.Unsetenv("WARDEN_AI_API_KEY")
	_ = os.Unsetenv("WARDEN_AI_API_KEY_FILE")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected partial model configuration to fail")
	}
}
