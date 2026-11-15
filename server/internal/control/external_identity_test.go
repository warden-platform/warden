package control

import (
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestDelegationTokenRoundTrip(t *testing.T) {
	id, token, hash, err := generateDelegationToken()
	if err != nil {
		t.Fatal(err)
	}
	parsedID, secret, ok := parseDelegationToken(token)
	if !ok || parsedID != id || secret == "" {
		t.Fatalf("parse delegation token = %q %q %v", parsedID, secret, ok)
	}
	if len(hash) != 32 {
		t.Fatalf("delegation token hash length = %d", len(hash))
	}
	if _, _, ok := parseDelegationToken("pam_dtk_" + id); ok {
		t.Fatal("truncated delegation token was accepted")
	}
	if _, _, ok := parseDelegationToken("pam_agk_" + id + "_secret"); ok {
		t.Fatal("agent API key was accepted as delegation token")
	}
}

func TestValidateExternalBindingInput(t *testing.T) {
	validAgent := externalBindingRequest{
		Source: "federated", WorkspaceID: "workspace-1", ExternalProjectID: "external-project-1",
		IdentityType: domain.ExternalIdentityAgent, ExternalID: "agent-1", PrincipalID: "principal-1", ProjectID: "project-1",
	}
	if err := validateExternalBindingInput(validAgent); err != nil {
		t.Fatal(err)
	}
	invalid := validAgent
	invalid.IdentityType = domain.ExternalIdentityUser
	if err := validateExternalBindingInput(invalid); err == nil {
		t.Fatal("USER binding with project was accepted")
	}
	invalid = validAgent
	invalid.ProjectID = ""
	if err := validateExternalBindingInput(invalid); err == nil {
		t.Fatal("AGENT binding without project was accepted")
	}
}
