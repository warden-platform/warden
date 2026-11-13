package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTaskEnvelopeSignAndVerify(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	now := time.Now().UTC()
	task := TaskEnvelope{
		Version: 1, ExecutionID: "execution-1", Attempt: 1, DispatchToken: "dispatch-1", ConnectorID: "connector-1",
		Resource:   TaskResource{ID: "resource-1", Name: "api", Namespace: "test"},
		Action:     TaskAction{Name: "deployment.get", Version: 1},
		Parameters: map[string]any{"name": "api", "namespace": "test"},
		Grant:      TaskGrant{ID: "grant-1", ScopeHash: "scope", ExpiresAt: now.Add(time.Minute)},
		IssuedAt:   now,
	}
	task.ParameterHash = HashParameters(task.Parameters)
	if err := task.Sign(key); err != nil {
		t.Fatal(err)
	}
	if err := task.Verify(key, now); err != nil {
		t.Fatal(err)
	}
	task.Parameters["name"] = "other"
	if err := task.Verify(key, now); err == nil {
		t.Fatal("tampered task was accepted")
	}
}

func TestTaskEnvelopeRejectsExpiredGrant(t *testing.T) {
	now := time.Now().UTC()
	task := TaskEnvelope{Version: 1, IssuedAt: now, Grant: TaskGrant{ExpiresAt: now.Add(-time.Second)}}
	if err := task.Verify([]byte("01234567890123456789012345678901"), now); err == nil {
		t.Fatal("expired task was accepted")
	}
}

func TestTaskEnvelopeSignsChangeSafetyContract(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	now := time.Now().UTC()
	contract := ActionSafetyContract("deployment.update_image", RiskHigh)
	task := TaskEnvelope{Version: 1, ExecutionID: "execution-1", Attempt: 1, DispatchToken: "dispatch-1",
		Parameters: map[string]any{}, ParameterHash: HashParameters(map[string]any{}), Safety: &contract,
		Grant: TaskGrant{ExpiresAt: now.Add(time.Minute)}, IssuedAt: now}
	if err := task.Sign(key); err != nil {
		t.Fatal(err)
	}
	task.Safety.RollbackMode = RollbackUnsupported
	if err := task.Verify(key, now); err == nil {
		t.Fatal("tampered change safety contract was accepted")
	}
}

func TestTaskEnvelopeSignsApprovalRequestIdentity(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	now := time.Now().UTC()
	parameters := map[string]any{QueryApprovalContextParameter: map[string]any{"requestId": "request-approved"}}
	task := TaskEnvelope{
		Version: 1, ExecutionID: "execution-1", Attempt: 1, DispatchToken: "dispatch-1",
		Parameters: parameters, ParameterHash: HashParameters(parameters),
		Grant: TaskGrant{ID: "grant-1", ExpiresAt: now.Add(time.Minute)}, IssuedAt: now,
	}
	if err := task.Sign(key); err != nil {
		t.Fatal(err)
	}
	if err := task.Verify(key, now); err != nil {
		t.Fatal(err)
	}
	task.Parameters[QueryApprovalContextParameter] = map[string]any{"requestId": "forged-request"}
	if err := task.Verify(key, now); err == nil {
		t.Fatal("approval request identity was not covered by the task signature")
	}
}

func TestTaskEnvelopeSignsProviderBinding(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	now := time.Now().UTC()
	parameters := map[string]any{"providerRequest": map[string]any{"path": "/api/dashboards/uid/payments"}}
	task := TaskEnvelope{Version: 1, ExecutionID: "execution-1", Attempt: 1, DispatchToken: "dispatch-1",
		Parameters: parameters, ParameterHash: HashParameters(parameters), Grant: TaskGrant{ExpiresAt: now.Add(time.Minute)}, IssuedAt: now,
		Provider: &ProviderTask{Request: ProviderRequest{Version: 1, ResourceID: "resource-1", Capability: "grafana.dashboard.read@v1", Method: "GET", Path: "/api/dashboards/uid/payments"},
			Binding: ProviderRequestBinding{Integration: Integration{ID: "integration-1", Endpoint: "https://grafana.example.com"}}}}
	if err := task.Sign(key); err != nil {
		t.Fatal(err)
	}
	task.Provider.Binding.Integration.Endpoint = "https://evil.example"
	if err := task.Verify(key, now); err == nil {
		t.Fatal("tampered provider binding was accepted")
	}
}

func TestProviderParameterHashSurvivesJSONRoundTrip(t *testing.T) {
	parameters := map[string]any{
		"providerRequest": ProviderRequest{Version: 1, ResourceID: "resource-1", Capability: "grafana.dashboard.read@v1", Method: "GET", Path: "/api/dashboards/uid/payments"},
		"providerBinding": ProviderRequestBinding{Integration: Integration{ID: "integration-1", Endpoint: "https://grafana.example.com"}},
	}
	payload, err := json.Marshal(parameters)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		t.Fatal(err)
	}

	expected := HashParameters(normalized)
	persisted, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal(persisted, &restored); err != nil {
		t.Fatal(err)
	}
	if got := HashParameters(restored); got != expected {
		t.Fatalf("provider parameter hash changed after JSON round trip: got %s, want %s", got, expected)
	}
}
