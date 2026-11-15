package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---

// insertDiagnosticResource seeds a native-Kubernetes deployment resource backed
// by a live connector that advertises the provider.request capability, so the
// resource appears in the ACTIVE diagnostic directory.
func insertDiagnosticResource(t *testing.T, pool *pgxpool.Pool, name, namespace, environment string) string {
	t.Helper()
	ctx := context.Background()
	var connectorID string
	err := pool.QueryRow(ctx, `INSERT INTO connectors (name, connector_type, status, environment, version, capabilities, last_heartbeat, tenant_id)
		VALUES ($1, 'kubernetes', 'ACTIVE', $2, '1.0.0',
		        '["provider.request@v1","provider.write@v1","pam.change-safety@v1"]'::jsonb, now(), 'platform')
		ON CONFLICT (name, tenant_id) DO UPDATE SET status='ACTIVE', environment=EXCLUDED.environment,
			capabilities=EXCLUDED.capabilities, last_heartbeat=EXCLUDED.last_heartbeat
		RETURNING id`, "diag-"+name+"-conn", environment).Scan(&connectorID)
	if err != nil {
		t.Fatalf("insert diagnostic connector: %v", err)
	}
	var resourceID string
	err = pool.QueryRow(ctx, `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, environment, status, tenant_id)
		VALUES ('deployment', $1, $2, $3, $4, $5, 'ACTIVE', 'platform')
		ON CONFLICT (connector_id, resource_type, external_id) DO UPDATE SET environment=EXCLUDED.environment, status='ACTIVE'
		RETURNING id`, "ext-"+name, name, namespace, connectorID, environment).Scan(&resourceID)
	if err != nil {
		t.Fatalf("insert diagnostic resource: %v", err)
	}
	return resourceID
}

// insertExecutionRow writes a terminal execution directly so classification can
// be asserted without running a live Connector. It references the seeded admin
// principal and deployment.get action.
func insertExecutionRow(t *testing.T, pool *pgxpool.Pool, resourceID, traceID, status, errorCode string, result map[string]any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO executions
		(tenant_id, principal_id, resource_id, action_id, parameters, parameter_hash, status, trace_id, error_code, result, connector_id)
		VALUES ('platform', '00000000-0000-0000-0000-000000000003', $1, 'a0000000-0000-0000-0000-000000000001',
		        '{}'::jsonb, 'diagnostic-test-hash', $2, $3, NULLIF($4,''), $5, (SELECT connector_id FROM resources WHERE id=$1))`,
		resourceID, status, traceID, errorCode, result)
	if err != nil {
		t.Fatalf("insert diagnostic execution: %v", err)
	}
}

func TestDiagnosticContextEndpoint(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	insertDiagnosticResource(t, pool, "web-app", "default", "staging")
	insertDiagnosticResource(t, pool, "db-app", "default", "production")

	resp := doAdminRequest(t, mux, http.MethodPost, "/api/v1/diagnostics",
		`{"traceId":"trace-ctx-1","environment":"staging"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /diagnostics returned %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		TraceID string `json:"traceId"`
		Summary struct {
			Total         int            `json:"total"`
			ByEnvironment map[string]int `json:"byEnvironment"`
		} `json:"summary"`
		Resources []struct {
			Name             string   `json:"name"`
			Environment      string   `json:"environment"`
			AvailableActions []string `json:"availableActions"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode diagnostics context: %v", err)
	}
	if body.TraceID != "trace-ctx-1" {
		t.Fatalf("traceId = %q, want trace-ctx-1", body.TraceID)
	}
	if body.Summary.Total != 1 {
		t.Fatalf("total = %d, want 1 (environment=staging)", body.Summary.Total)
	}
	if len(body.Resources) != 1 || body.Resources[0].Name != "web-app" || body.Resources[0].Environment != "staging" {
		t.Fatalf("unexpected filtered resources: %#v", body.Resources)
	}
	if !containsString(body.Resources[0].AvailableActions, "provider.request") {
		t.Fatalf("availableActions missing provider.request: %#v", body.Resources[0].AvailableActions)
	}
}

func TestDiagnosticTracePassthroughAndAggregation(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	resourceID := insertDiagnosticResource(t, pool, "web-app", "default", "testing")

	params := map[string]any{
		"providerRequest": map[string]any{
			"version": 1, "resourceId": resourceID, "capability": "kubernetes.resource.read@v1",
			"method": "GET", "path": "/api/v1/namespaces/default/deployments/web-app",
		},
	}
	for i := 0; i < 2; i++ {
		payload, err := json.Marshal(map[string]any{
			"resourceId":    resourceID,
			"action":        "provider.request",
			"actionVersion": 1,
			"parameters":    params,
			"traceId":       "trace-shared-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		resp := doAdminRequest(t, mux, http.MethodPost, "/api/v1/executions", string(payload))
		if resp.Code != http.StatusAccepted {
			t.Fatalf("create execution %d returned %d: %s", i+1, resp.Code, resp.Body.String())
		}
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM executions WHERE trace_id='trace-shared-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("executions sharing trace_id = %d, want 2", count)
	}

	resp := doAdminRequest(t, mux, http.MethodGet, "/api/v1/diagnostics/trace-shared-1", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /diagnostics returned %d: %s", resp.Code, resp.Body.String())
	}
	var result struct {
		TraceID string `json:"traceId"`
		Results []struct {
			Resource struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"resource"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode diagnostics result: %v", err)
	}
	if result.TraceID != "trace-shared-1" || len(result.Results) != 2 {
		t.Fatalf("traceId=%q results=%d, want trace-shared-1 / 2", result.TraceID, len(result.Results))
	}
	if result.Results[0].Resource.Name != "web-app" || result.Results[0].Resource.Type != "deployment" {
		t.Fatalf("unexpected resource identity: %#v", result.Results[0].Resource)
	}
	assertNoInternalIDs(t, resp.Body.String())
}

func TestDiagnosticResultClassification(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	resourceID := insertDiagnosticResource(t, pool, "web-app", "default", "testing")

	insertExecutionRow(t, pool, resourceID, "trace-classify-1", string(domain.ExecSucceeded), "", map[string]any{"status": "SUCCEEDED"})
	insertExecutionRow(t, pool, resourceID, "trace-classify-1", string(domain.ExecSucceeded), "", map[string]any{"status": "NO_DATA"})
	insertExecutionRow(t, pool, resourceID, "trace-classify-1", string(domain.ExecFailed), domain.ProviderErrTimeout, map[string]any{})
	insertExecutionRow(t, pool, resourceID, "trace-classify-1", string(domain.ExecFailed), domain.ProviderErrMethodDenied, map[string]any{})

	resp := doAdminRequest(t, mux, http.MethodGet, "/api/v1/diagnostics/trace-classify-1", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /diagnostics returned %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Summary map[string]int `json:"classificationSummary"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode classification summary: %v", err)
	}
	want := map[string]int{
		"SUCCEEDED": 1, "NO_DATA": 1, "TIMEOUT": 1, "NO_PERMISSION": 1,
	}
	for class, n := range want {
		if body.Summary[class] != n {
			t.Fatalf("classificationSummary[%s] = %d, want %d (full=%v)", class, body.Summary[class], n, body.Summary)
		}
	}
	assertNoInternalIDs(t, resp.Body.String())
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func assertNoInternalIDs(t *testing.T, body string) {
	t.Helper()
	for _, key := range []string{"executionId", "actionId", "grantId", "decisionId", "credentialId"} {
		if strings.Contains(body, key) {
			t.Fatalf("diagnostic result leaks internal id key %q: %s", key, body)
		}
	}
}
