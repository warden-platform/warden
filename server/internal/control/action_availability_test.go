package control

import (
	"context"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func TestResourceActionAvailabilityRequiresCapabilityUpgrade(t *testing.T) {
	now := time.Now()
	resource := &domain.Resource{Status: domain.ResourceActive}
	connector := &domain.Connector{
		Status:        domain.ConnectorActive,
		LastHeartbeat: &now,
		Capabilities:  []string{"deployment.get@v1"},
	}
	action := domain.ActionDefinition{Name: "deployment.get", Version: 2, RiskLevel: domain.RiskLow}
	available, code, reason := resourceActionAvailability(context.Background(), &Handler{}, resource, connector, action)
	if available || code != "CONNECTOR_UPGRADE_REQUIRED" || reason == "" {
		t.Fatalf("resourceActionAvailability() = (%v, %q, %q), want upgrade required", available, code, reason)
	}
}

func TestResourceActionAvailabilityAcceptsLegacyCapability(t *testing.T) {
	now := time.Now()
	resource := &domain.Resource{Status: domain.ResourceActive}
	connector := &domain.Connector{
		Status:        domain.ConnectorActive,
		LastHeartbeat: &now,
		Capabilities:  []string{"deployment.get"},
	}
	action := domain.ActionDefinition{Name: "deployment.get", Version: 2, RiskLevel: domain.RiskLow}
	available, code, reason := resourceActionAvailability(context.Background(), &Handler{}, resource, connector, action)
	if !available || code != "" || reason != "" {
		t.Fatalf("resourceActionAvailability() = (%v, %q, %q), want available", available, code, reason)
	}
}

func TestResourceActionAvailabilityRequiresChangeSafetyCapability(t *testing.T) {
	now := time.Now()
	resource := &domain.Resource{Status: domain.ResourceActive}
	connector := &domain.Connector{Status: domain.ConnectorActive, LastHeartbeat: &now, Capabilities: []string{"deployment.update_image@v1"}}
	action := domain.ActionDefinition{Name: "deployment.update_image", Version: 1, RiskLevel: domain.RiskHigh}
	available, code, _ := resourceActionAvailability(context.Background(), &Handler{}, resource, connector, action)
	if available || code != "CONNECTOR_UPGRADE_REQUIRED" {
		t.Fatalf("resourceActionAvailability() = (%v, %q), want safety upgrade", available, code)
	}
}

func TestH3CInterfaceActionScopeAvailability(t *testing.T) {
	if available, code, _ := h3cInterfaceActionScopeAvailability("network.interface.description.update", nil); available || code != "NETWORK_CHANGES_DISABLED" {
		t.Fatalf("disabled mutation availability = (%v, %q), want NETWORK_CHANGES_DISABLED", available, code)
	}
	if available, code, reason := h3cInterfaceActionScopeAvailability("network.interface.description.preview", nil); !available || code != "" || reason != "" {
		t.Fatalf("preview availability = (%v, %q, %q), want available", available, code, reason)
	}
	if available, code, reason := h3cInterfaceActionScopeAvailability("network.interface.access_vlan.update", map[string]any{"allowInterfaceChanges": true}); !available || code != "" || reason != "" {
		t.Fatalf("enabled mutation availability = (%v, %q, %q), want available", available, code, reason)
	}
}

func TestH3CInterfaceMutationRequiresChangeSafetyCapability(t *testing.T) {
	now := time.Now()
	resource := &domain.Resource{ResourceType: "network_interface", Status: domain.ResourceActive}
	connector := &domain.Connector{Status: domain.ConnectorActive, LastHeartbeat: &now, Capabilities: []string{"network.interface.description.update@v1"}}
	action := domain.ActionDefinition{Name: "network.interface.description.update", Version: 1, ResourceType: "network_interface", RiskLevel: domain.RiskHigh}
	available, code, reason := resourceActionAvailability(context.Background(), &Handler{}, resource, connector, action)
	if available || code != "CONNECTOR_UPGRADE_REQUIRED" || reason == "" {
		t.Fatalf("mutation availability = (%v, %q, %q), want safety upgrade", available, code, reason)
	}
}

func TestH3CInterfacePreviewDoesNotRequireChangeSafetyCapability(t *testing.T) {
	now := time.Now()
	resource := &domain.Resource{ResourceType: "network_interface", Status: domain.ResourceActive}
	connector := &domain.Connector{Status: domain.ConnectorActive, LastHeartbeat: &now, Capabilities: []string{"network.interface.description.preview@v1"}}
	action := domain.ActionDefinition{Name: "network.interface.description.preview", Version: 1, ResourceType: "network_interface", RiskLevel: domain.RiskLow}
	available, code, reason := resourceActionAvailability(context.Background(), &Handler{}, resource, connector, action)
	if !available || code != "" || reason != "" {
		t.Fatalf("preview availability = (%v, %q, %q), want available", available, code, reason)
	}
}

func TestJenkinsControllerCreateActionAvailability(t *testing.T) {
	now := time.Now()
	resource := &domain.Resource{ResourceType: "jenkins_controller", Status: domain.ResourceActive}
	connector := &domain.Connector{Status: domain.ConnectorActive, LastHeartbeat: &now, Capabilities: []string{"jenkins.job.create@v1", domain.ChangeSafetyCapability}}
	action := domain.ActionDefinition{Name: "jenkins.job.create", Version: 1, ResourceType: "jenkins_controller", RiskLevel: domain.RiskHigh}
	available, code, reason := resourceActionAvailability(context.Background(), &Handler{}, resource, connector, action)
	if !available || code != "" || reason != "" {
		t.Fatalf("controller create availability = (%v, %q, %q), want available", available, code, reason)
	}
}

func TestGenericKubernetesActionProjection(t *testing.T) {
	tests := []struct {
		resourceType string
		action       string
		want         bool
	}{
		{"namespace", "kubernetes.resource.list", true},
		{"namespace", "kubernetes.resource.create", true},
		{"namespace", "kubernetes.resource.update", false},
		{"deployment", "kubernetes.resource.get", true},
		{"deployment", "kubernetes.resource.update", true},
		{"networkpolicy", "kubernetes.resource.delete", true},
		{"poddisruptionbudget", "kubernetes.resource.preview", true},
		{"pod", "kubernetes.resource.update", false},
		{"secret", "kubernetes.resource.get", false},
		{"jenkins_job", "kubernetes.resource.get", false},
	}
	for _, tt := range tests {
		if got := genericKubernetesActionApplies(tt.resourceType, tt.action); got != tt.want {
			t.Errorf("genericKubernetesActionApplies(%q, %q) = %v, want %v", tt.resourceType, tt.action, got, tt.want)
		}
	}
}

func TestResourceSupportsProviderRequest(t *testing.T) {
	tests := []struct {
		resourceType string
		protocol     string
		want         bool
	}{
		{"grafana_dashboard", "grafana", true},
		{"jenkins_job", "jenkins", true},
		{"deployment", "kubernetes", true},
		{"huawei_ecs_instance", "huaweicloud", true},
		{"huawei_obs_bucket", "huaweicloud", true},
		{"systemd_service", "kubernetes", false},
		{"jenkins_job", "", false},
		{"deployment", "", false},
	}
	for _, tt := range tests {
		if got := resourceSupportsProviderRequest(tt.resourceType, tt.protocol); got != tt.want {
			t.Errorf("resourceSupportsProviderRequest(%q, %q) = %v, want %v", tt.resourceType, tt.protocol, got, tt.want)
		}
	}
}

func TestValidateGenericKubernetesParametersRejectsUnsafeInput(t *testing.T) {
	resource := &domain.Resource{ResourceType: "namespace", Name: "warden-e2e-7213-test"}
	safe := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "manifest": map[string]any{"data": map[string]any{"mode": "test"}}}
	if err := validateGenericKubernetesParameters(safe, resource); err != nil {
		t.Fatalf("safe parameters rejected: %v", err)
	}
	unsafe := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "manifest": map[string]any{"data": map[string]any{"accessToken": "must-not-persist"}}}
	if err := validateGenericKubernetesParameters(unsafe, resource); err == nil {
		t.Fatal("sensitive manifest was accepted")
	}
	privateKey := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "manifest": map[string]any{"data": map[string]any{"tls.key": "-----BEGIN PRIVATE KEY-----\nredacted\n-----END PRIVATE KEY-----"}}}
	if err := validateGenericKubernetesParameters(privateKey, resource); err == nil {
		t.Fatal("private key material was accepted")
	}
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret"}
	if err := validateGenericKubernetesParameters(secret, resource); err == nil {
		t.Fatal("Secret GVK was accepted")
	}
	deployment := &domain.Resource{ResourceType: "deployment"}
	wrongKind := map[string]any{"apiVersion": "v1", "kind": "ConfigMap"}
	if err := validateGenericKubernetesParameters(wrongKind, deployment); err == nil {
		t.Fatal("GVK that does not match the bound resource was accepted")
	}
}
