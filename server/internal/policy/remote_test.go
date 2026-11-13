package policy

import (
	"os"
	"testing"
)

func TestRemoteProviderRequiresSecureTransport(t *testing.T) {
	for _, name := range []string{"PAM_OPA_BEARER_TOKEN", "PAM_OPA_BEARER_TOKEN_FILE"} {
		value, existed := os.LookupEnv(name)
		_ = os.Unsetenv(name)
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
	t.Setenv("PAM_ALLOW_INSECURE_OPA", "")
	t.Setenv("OPA_ENDPOINT", "http://opa.example.test:8181")
	if _, err := NewRemoteProvider(); err == nil {
		t.Fatal("remote OPA accepted insecure non-loopback HTTP")
	}
	t.Setenv("OPA_ENDPOINT", "https://opa.example.test")
	if _, err := NewRemoteProvider(); err != nil {
		t.Fatalf("remote OPA rejected HTTPS: %v", err)
	}
	t.Setenv("OPA_ENDPOINT", "http://127.0.0.1:8181")
	if _, err := NewRemoteProvider(); err != nil {
		t.Fatalf("remote OPA rejected loopback development endpoint: %v", err)
	}
}
