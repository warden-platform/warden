package control

import (
	"testing"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func TestValidateActionParameters(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"lines": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
		},
		"required": []any{"lines"},
	}
	if err := validateActionParameters(schema, map[string]any{"lines": float64(10)}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []map[string]any{
		{},
		{"lines": float64(0)},
		{"lines": float64(10), "unexpected": true},
	} {
		if err := validateActionParameters(schema, invalid); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", invalid)
		}
	}
}

func TestValidateResourceParametersOnlyChecksDeclaredIdentityFields(t *testing.T) {
	resource := &domain.Resource{Name: "server-1", Namespace: "namespace-1"}
	if err := validateResourceParameters(map[string]any{}, resource); err != nil {
		t.Fatalf("schema without identity fields should be accepted: %v", err)
	}
	if err := validateResourceParameters(map[string]any{"name": "server-1", "namespace": "namespace-1"}, resource); err != nil {
		t.Fatalf("matching identity fields rejected: %v", err)
	}
	for _, parameters := range []map[string]any{
		{"name": "another-server"},
		{"namespace": "another-namespace"},
	} {
		if err := validateResourceParameters(parameters, resource); err == nil {
			t.Fatalf("mismatched identity was accepted: %#v", parameters)
		}
	}
}

func TestValidateJenkinsResourceParametersLocksJobIdentity(t *testing.T) {
	resource := &domain.Resource{ResourceType: "jenkins_job", Name: "example-prod/warden-release"}
	if err := validateJenkinsResourceParameters("jenkins.job.configure", map[string]any{"name": resource.Name, "description": "safe"}, resource); err != nil {
		t.Fatalf("matching Jenkins target rejected: %v", err)
	}
	for _, parameters := range []map[string]any{
		{"name": "another-job"},
		{"jobName": "another-job"},
		{"fullName": "another-job"},
	} {
		if err := validateJenkinsResourceParameters("jenkins.job.configure", parameters, resource); err == nil {
			t.Fatalf("cross-resource Jenkins target accepted: %#v", parameters)
		}
	}
	if err := validateJenkinsResourceParameters("jenkins.job.delete", map[string]any{"confirmJobName": "warden-release"}, resource); err == nil {
		t.Fatal("partial Jenkins delete confirmation was accepted")
	}
	if err := validateJenkinsResourceParameters("jenkins.job.delete", map[string]any{"confirmJobName": resource.Name}, resource); err != nil {
		t.Fatalf("exact Jenkins delete confirmation rejected: %v", err)
	}
	controller := &domain.Resource{ResourceType: "jenkins_controller", Name: "jenkins.example.com"}
	if err := validateJenkinsResourceParameters("jenkins.job.create", map[string]any{"name": "warden-e2e-7213-job"}, controller); err != nil {
		t.Fatalf("controller-bound create rejected: %v", err)
	}
}
