package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- DB-gated integration tests (PAM_TEST_DATABASE_URL) ---

func TestProviderWriteCapabilityRegistration(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)

	// A governed write capability registers normally.
	created := doAdminRequest(t, mux, http.MethodPost, "/api/v1/provider-capabilities", `{
		"name":"jenkins.job.trigger","version":1,"write":true,
		"resourceKinds":["jenkins_job"],"protocols":["jenkins"],"methods":["POST"],
		"pathPatterns":["/job/{job}/build"],"maxItems":10,"maxResponseBytes":1048576,
		"timeoutSeconds":30,"maxBodyBytes":65536,
		"requestBodySchema":{"type":"object","required":["branch"],"properties":{"branch":{"type":"string"}}},
		"description":"触发 Jenkins 构建"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create write capability returned %d: %s", created.Code, created.Body.String())
	}

	// DELETE is never admitted to a write capability.
	invalid := doAdminRequest(t, mux, http.MethodPost, "/api/v1/provider-capabilities", `{
		"name":"bad.delete","version":1,"write":true,
		"resourceKinds":["jenkins_job"],"protocols":["jenkins"],"methods":["DELETE"],
		"pathPatterns":["/job/{job}"],"maxBodyBytes":1024}`)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "PROVIDER_METHOD_DENIED") {
		t.Fatalf("DELETE write capability returned %d: %s", invalid.Code, invalid.Body.String())
	}

	// Native Kubernetes writes stay on the dedicated dry-run CRUD.
	k8s := doAdminRequest(t, mux, http.MethodPost, "/api/v1/provider-capabilities", `{
		"name":"bad.k8s.write","version":1,"write":true,
		"resourceKinds":["deployment"],"protocols":["kubernetes"],"methods":["PATCH"],
		"pathPatterns":["/api/v1/namespaces/{namespace}/deployments/{name}"],"maxBodyBytes":1024}`)
	if k8s.Code != http.StatusBadRequest || !strings.Contains(k8s.Body.String(), "PROVIDER_METHOD_DENIED") {
		t.Fatalf("kubernetes write capability returned %d: %s", k8s.Code, k8s.Body.String())
	}
}

func TestProviderWriteExecutionApprovalGate(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")

	resourceID := insertJenkinsWriteResource(t, pool)

	// The write action must be discoverable in the execution directory once a
	// live connector advertises provider.write@v1.
	available, err := h.actions.ListAvailable(ctx)
	if err != nil {
		t.Fatalf("list available actions: %v", err)
	}
	found := false
	for _, action := range available {
		if action.Name == "provider.write" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("provider.write is not advertised in the execution directory")
	}

	// Register the write capability that the signed binding will reference.
	created := doAdminRequest(t, mux, http.MethodPost, "/api/v1/provider-capabilities", `{
		"name":"jenkins.job.trigger","version":1,"write":true,
		"resourceKinds":["jenkins_job"],"protocols":["jenkins"],"methods":["POST"],
		"pathPatterns":["/job/{job}/build"],"maxItems":10,"maxResponseBytes":1048576,
		"timeoutSeconds":30,"maxBodyBytes":65536,
		"requestBodySchema":{"type":"object","required":["branch"],"properties":{"branch":{"type":"string"}}},
		"description":"触发 Jenkins 构建"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create write capability returned %d: %s", created.Code, created.Body.String())
	}

	// provider.write is high-risk, so it must land in WAITING_APPROVAL with a
	// signed binding persisted in executions.parameters.
	exec := createExecutionForResource(t, mux, resourceID, "provider.write", map[string]any{
		"providerRequest": map[string]any{
			"version": 1, "resourceId": resourceID, "capability": "jenkins.job.trigger@v1",
			"method": "POST", "path": "/job/platform/build",
			"body": map[string]any{"branch": "main"},
		},
	})
	if exec.Code != http.StatusAccepted {
		t.Fatalf("create returned %d: %s", exec.Code, exec.Body.String())
	}
	parsed := parseExecutionResponse(t, exec)
	if parsed.Status != string(domain.ExecWaitingApproval) {
		t.Fatalf("provider.write execution status = %q, want WAITING_APPROVAL", parsed.Status)
	}

	// The persisted parameters carry the signed Connector binding; the resolved
	// capability is the write entry with write=true and the body is intact.
	var parameters map[string]any
	if err := pool.QueryRow(ctx, `SELECT parameters FROM executions WHERE id=$1`, parsed.ID).Scan(&parameters); err != nil {
		t.Fatalf("load execution parameters: %v", err)
	}
	bindingRaw, ok := parameters["providerBinding"]
	if !ok {
		t.Fatal("execution parameters are missing the signed providerBinding")
	}
	bindingPayload, err := json.Marshal(bindingRaw)
	if err != nil {
		t.Fatal(err)
	}
	var binding domain.ProviderRequestBinding
	if err := json.Unmarshal(bindingPayload, &binding); err != nil {
		t.Fatal(err)
	}
	if !binding.Capability.Write {
		t.Fatal("signed binding resolved a non-write capability")
	}
	requestRaw, ok := parameters["providerRequest"]
	if !ok {
		t.Fatal("execution parameters are missing providerRequest")
	}
	requestPayload, err := json.Marshal(requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(requestPayload), `"branch":"main"`) {
		t.Fatalf("write body did not round-trip: %s", requestPayload)
	}

	// Approval issues a Grant and moves the execution out of WAITING_APPROVAL.
	approvePendingExecution(t, mux, pool, parsed.ID)
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM executions WHERE id=$1`, parsed.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == string(domain.ExecWaitingApproval) {
		t.Fatal("approved execution is still WAITING_APPROVAL")
	}
	var requestID string
	if err := pool.QueryRow(ctx, `SELECT id FROM access_requests WHERE execution_id=$1`, parsed.ID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	var approved string
	if err := pool.QueryRow(ctx, `SELECT status FROM access_requests WHERE id=$1`, requestID).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if approved != "APPROVED" {
		t.Fatalf("access request status = %q, want APPROVED", approved)
	}
	var grantCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_grants WHERE request_id=$1`, requestID).Scan(&grantCount); err != nil {
		t.Fatal(err)
	}
	if grantCount != 1 {
		t.Fatalf("grants = %d, want 1", grantCount)
	}
}

func insertJenkinsWriteResource(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors (name, connector_type, status, environment, version, capabilities, last_heartbeat, tenant_id)
		VALUES ('test-jenkins-connector', 'jenkins', 'ACTIVE', 'testing', '1.0.0', '["provider.write@v1","pam.change-safety@v1"]'::jsonb, now(), 'platform')
		ON CONFLICT (name, tenant_id) DO UPDATE SET status='ACTIVE', capabilities=EXCLUDED.capabilities, last_heartbeat=EXCLUDED.last_heartbeat
		RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	var integrationID string
	if err := pool.QueryRow(ctx, `INSERT INTO integrations (name, connector_id, protocol, endpoint, username, credential_ref, status, environment, tenant_id)
		VALUES ('jenkins-write-test', $1, 'jenkins', 'https://jenkins.example.com', '', 'jenkins-write-test-cred', 'ACTIVE', 'testing', 'platform')
		RETURNING id`, connectorID).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}
	var resourceID string
	if err := pool.QueryRow(ctx, `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, integration_id, environment, status, tenant_id)
		VALUES ('jenkins_job', 'job/platform', 'platform', '', $1, $2, 'testing', 'ACTIVE', 'platform')
		RETURNING id`, connectorID, integrationID).Scan(&resourceID); err != nil {
		t.Fatalf("insert resource: %v", err)
	}
	return resourceID
}
