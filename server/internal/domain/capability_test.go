package domain

import (
	"reflect"
	"testing"
)

func TestNormalizeCapabilities(t *testing.T) {
	got, err := NormalizeCapabilities([]string{" deployment.get@v1 ", "deployment.get@v1", "pod.get"})
	if err != nil {
		t.Fatalf("NormalizeCapabilities returned error: %v", err)
	}
	want := []string{"deployment.get@v1", "pod.get"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeCapabilities = %#v, want %#v", got, want)
	}
}

func TestNormalizeCapabilitiesRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "deployment.get@v0", "deployment.get@v1@v2", "Deployment.Get@v1", "deployment/get@v1"} {
		if _, err := NormalizeCapabilities([]string{value}); err == nil {
			t.Errorf("NormalizeCapabilities(%q) unexpectedly succeeded", value)
		}
	}
}

func TestSupportsAction(t *testing.T) {
	tests := []struct {
		name         string
		capabilities []string
		version      int
		want         bool
	}{
		{"exact version", []string{"deployment.get@v1"}, 1, true},
		{"legacy unversioned", []string{"deployment.get"}, 2, true},
		{"version mismatch", []string{"deployment.get@v1"}, 2, false},
		{"different action", []string{"pod.get@v1"}, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SupportsAction(tt.capabilities, "deployment.get", tt.version); got != tt.want {
				t.Fatalf("SupportsAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSummarizeActionAccess(t *testing.T) {
	actions := []ActionDefinition{
		{Name: "resource.get", Version: 1, RiskLevel: RiskLow},
		{Name: "resource.update", Version: 2, RiskLevel: RiskMedium},
		{Name: "resource.delete", Version: 1, RiskLevel: RiskHigh},
	}
	tests := []struct {
		name        string
		caps        []string
		wantAccess  ActionAccess
		wantCount   int
		wantUpgrade bool
	}{
		{"read only", []string{"resource.get@v1"}, ActionAccessReadOnly, 1, true},
		{"change", []string{ChangeSafetyCapability, "resource.get@v1", "resource.update@v2"}, ActionAccessChange, 2, true},
		{"restricted", []string{ChangeSafetyCapability, "resource.get@v1", "resource.update@v2", "resource.delete@v1"}, ActionAccessRestrictedChange, 3, false},
		{"version mismatch", []string{"resource.update@v1"}, ActionAccessReadOnly, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access, count, upgrade := SummarizeActionAccess(tt.caps, actions)
			if access != tt.wantAccess || count != tt.wantCount || upgrade != tt.wantUpgrade {
				t.Fatalf("SummarizeActionAccess() = (%s, %d, %v), want (%s, %d, %v)", access, count, upgrade, tt.wantAccess, tt.wantCount, tt.wantUpgrade)
			}
		})
	}
}
