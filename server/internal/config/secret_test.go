package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET_FILE", path)
	value, err := Secret("TEST_SECRET")
	if err != nil || value != "file-secret" {
		t.Fatalf("Secret() = %q, %v", value, err)
	}
}

func TestSecretRejectsAmbiguousSources(t *testing.T) {
	t.Setenv("TEST_SECRET", "inline")
	t.Setenv("TEST_SECRET_FILE", filepath.Join(t.TempDir(), "secret"))
	_, err := Secret("TEST_SECRET")
	if err == nil || !strings.Contains(err.Error(), "cannot both be set") {
		t.Fatalf("expected ambiguous source error, got %v", err)
	}
}

func TestRequiredSecretRejectsShortValue(t *testing.T) {
	t.Setenv("TEST_SECRET", "short")
	_, err := RequiredSecret("TEST_SECRET", 32)
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("expected length error, got %v", err)
	}
}
