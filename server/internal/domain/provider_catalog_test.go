package domain

import (
	"net/http"
	"testing"
)

func TestBuiltinProviderCapabilitiesAreValidAndReadOnly(t *testing.T) {
	seen := map[string]bool{}
	for _, capability := range BuiltinProviderCapabilities {
		if seen[capability.Key()] {
			t.Fatalf("duplicate capability %s", capability.Key())
		}
		seen[capability.Key()] = true
		if err := ValidateProviderCapability(capability); err != nil {
			t.Errorf("capability %s is invalid: %v", capability.Key(), err)
		}
	}
}

func TestFindProviderCapabilityScopesKindAndProtocol(t *testing.T) {
	if _, ok := FindProviderCapability("grafana.dashboard.read@v1", "grafana_dashboard", "grafana"); !ok {
		t.Fatal("registered Grafana capability not found")
	}
	if _, ok := FindProviderCapability("grafana.dashboard.read@v1", "jenkins_job", "grafana"); ok {
		t.Fatal("capability crossed its resource kind")
	}
	if _, ok := FindProviderCapability("grafana.dashboard.read@v1", "grafana_dashboard", "jenkins"); ok {
		t.Fatal("capability crossed its protocol")
	}
}

func TestFindProviderCapabilityInUsesProvidedCatalog(t *testing.T) {
	catalog := append([]ResourceCapability(nil), BuiltinProviderCapabilities...)
	catalog = append(catalog, ResourceCapability{Name: "jenkins.build.read", Version: 1,
		ResourceKinds: []string{"jenkins_job"}, Protocols: []string{"jenkins"},
		Methods: []string{http.MethodGet}, PathPatterns: []string{"/job/{job}/api/json"},
		MaxItems: 10, MaxResponseBytes: 1 << 20, TimeoutSeconds: 10})
	capability, ok := FindProviderCapabilityIn(catalog, "jenkins.build.read@v1", "jenkins_job", "jenkins")
	if !ok {
		t.Fatal("registered catalog entry not found")
	}
	if capability.Name != "jenkins.build.read" {
		t.Fatalf("unexpected capability %q", capability.Name)
	}
	if _, ok := FindProviderCapabilityIn(catalog, "jenkins.build.read@v2", "jenkins_job", "jenkins"); ok {
		t.Fatal("unversioned mismatch resolved")
	}
	// The built-in catalog must not contain the entry yet (DB is the registry).
	if _, ok := FindProviderCapability("jenkins.build.read@v1", "jenkins_job", "jenkins"); ok {
		t.Fatal("custom entry leaked into the built-in catalog")
	}
}

func TestProviderCapabilitiesForKindsFiltersByKindAndProtocol(t *testing.T) {
	result := ProviderCapabilitiesForKinds(BuiltinProviderCapabilities, "deployment", "kubernetes")
	if len(result) != 1 || result[0].Name != "kubernetes.resource.read" {
		t.Fatalf("unexpected kubernetes capabilities: %v", result)
	}
	if got := ProviderCapabilitiesForKinds(BuiltinProviderCapabilities, "systemd_service", "kubernetes"); len(got) != 0 {
		t.Fatalf("systemd_service unexpectedly has capabilities: %v", got)
	}
}

func TestSplitCapabilityKey(t *testing.T) {
	cases := []struct {
		key     string
		name    string
		version int
		ok      bool
	}{
		{"grafana.dashboard.read@v1", "grafana.dashboard.read", 1, true},
		{"provider.request", "provider.request", 0, true},
		{"deployment.get@v2", "deployment.get", 2, true},
		{"bad key@v1", "", 0, false},
		{"x@v0", "", 0, false},
		{"x@v1.5", "", 0, false},
		{"", "", 0, false},
	}
	for _, tt := range cases {
		name, version, ok := SplitCapabilityKey(tt.key)
		if name != tt.name || version != tt.version || ok != tt.ok {
			t.Errorf("SplitCapabilityKey(%q) = (%q,%d,%v), want (%q,%d,%v)",
				tt.key, name, version, ok, tt.name, tt.version, tt.ok)
		}
	}
}
