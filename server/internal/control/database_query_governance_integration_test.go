package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-gated integration coverage for the complete control-plane transition:
// trusted preview verdict -> immutable approval floor -> access request.
func TestDatabaseQueryCostVerdictCreatesApproval(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")
	resourceID, connectorID := insertDatabaseQueryResource(t, pool)
	previewHash := strings.Repeat("a", 64)
	insertDatabaseQueryPreview(t, pool, resourceID, connectorID, previewHash, databaseCostRequireApproval)

	resp := createExecutionForResource(t, mux, resourceID, "database.query.execute", map[string]any{
		"ast":         map[string]any{"astVersion": 1},
		"previewHash": previewHash,
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("create returned %d: %s", resp.Code, resp.Body.String())
	}
	exec := parseExecutionResponse(t, resp)
	if exec.Status != string(domain.ExecWaitingApproval) {
		t.Fatalf("execution status = %q, want WAITING_APPROVAL", exec.Status)
	}
	var requestID string
	if err := pool.QueryRow(ctx, `SELECT id FROM access_requests WHERE execution_id=$1 AND status='PENDING'`, exec.ID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	var costFloor bool
	if err := pool.QueryRow(ctx, `SELECT COALESCE((d.opa_output->>'database_query_cost_floor')::boolean,false)
		FROM policy_decisions d JOIN executions e ON e.decision_id=d.id WHERE e.id=$1`, exec.ID).Scan(&costFloor); err != nil {
		t.Fatal(err)
	}
	if !costFloor {
		t.Fatal("policy decision did not persist the database query cost floor")
	}
	approvePendingExecution(t, mux, pool, exec.ID)
	tasks, err := h.executions.ClaimTasks(ctx, connectorID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(tasks))
	}
	proof, _ := tasks[0].Parameters[domain.QueryApprovalContextParameter].(map[string]any)
	if proof["requestId"] != requestID {
		t.Fatalf("approved task proof = %#v, want requestId %s", proof, requestID)
	}
}

func TestDatabaseQueryExecuteRejectsUntrustedPreviewHash(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	resourceID, _ := insertDatabaseQueryResource(t, pool)
	resp := createExecutionForResource(t, mux, resourceID, "database.query.execute", map[string]any{
		"ast":         map[string]any{"astVersion": 1},
		"previewHash": strings.Repeat("b", 64),
	})
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "QUERY_PREVIEW_REQUIRED") {
		t.Fatalf("untrusted preview returned %d: %s", resp.Code, resp.Body.String())
	}
}

func TestRuntimeDatabaseQueryCostEscalationCreatesApproval(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")
	resourceID, connectorID := insertDatabaseQueryResource(t, pool)
	previewHash := strings.Repeat("c", 64)
	insertDatabaseQueryPreview(t, pool, resourceID, connectorID, previewHash, databaseCostAllow)

	resp := createExecutionForResource(t, mux, resourceID, "database.query.execute", map[string]any{
		"ast": map[string]any{"astVersion": 1}, "previewHash": previewHash,
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("create returned %d: %s", resp.Code, resp.Body.String())
	}
	exec := parseExecutionResponse(t, resp)
	if exec.Status != string(domain.ExecQueued) {
		t.Fatalf("initial execution status = %q, want QUEUED", exec.Status)
	}
	tasks, err := h.executions.ClaimTasks(ctx, connectorID, 1)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("claim task: count=%d err=%v", len(tasks), err)
	}
	payload, err := json.Marshal(map[string]any{
		"dispatchToken": tasks[0].DispatchToken,
		"status":        "FAILED",
		"error":         "DATABASE_APPROVAL_REQUIRED: runtime EXPLAIN changed",
		"result": map[string]any{
			"previewHash": previewHash, "verdict": databaseCostRequireApproval,
			"estimatedRows": 25000, "estimatedCost": 1200, "fullScan": true,
			"decisionReasons": []string{"SOFT_ROWS_EXCEEDED", "SOFT_COST_EXCEEDED", "FULL_SCAN"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resultReq := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/"+connectorID+"/tasks/"+exec.ID+"/result", bytes.NewReader(payload))
	resultReq = resultReq.WithContext(WithTenantContext(resultReq.Context(), "platform"))
	resultReq.SetPathValue("id", connectorID)
	resultReq.SetPathValue("executionId", exec.ID)
	resultResp := httptest.NewRecorder()
	h.SubmitTaskResult(resultResp, resultReq)
	if resultResp.Code != http.StatusOK {
		t.Fatalf("runtime escalation returned %d: %s", resultResp.Code, resultResp.Body.String())
	}
	current, err := h.executions.GetByID(ctx, exec.ID)
	if err != nil || current.Status != domain.ExecWaitingApproval {
		t.Fatalf("runtime-escalated execution = %#v, err=%v", current, err)
	}
	request, err := h.requests.GetPendingByExecutionID(ctx, exec.ID)
	if err != nil || request == nil {
		t.Fatalf("runtime approval request = %#v, err=%v", request, err)
	}
	approvePendingExecution(t, mux, pool, exec.ID)
	tasks, err = h.executions.ClaimTasks(ctx, connectorID, 1)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("claim approved runtime task: count=%d err=%v", len(tasks), err)
	}
	proof, _ := tasks[0].Parameters[domain.QueryApprovalContextParameter].(map[string]any)
	if proof["requestId"] != request.ID {
		t.Fatalf("runtime-approved task proof = %#v, want requestId %s", proof, request.ID)
	}
}

func insertDatabaseQueryResource(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := domain.WithTenantID(context.Background(), "platform")
	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors (name, connector_type, status, environment, version, capabilities, last_heartbeat, tenant_id)
		VALUES ('database-query-governance-test', 'database', 'ACTIVE', 'testing', '1.0.0',
			'["database.query.preview@v1","database.query.execute@v1"]'::jsonb, now(), 'platform')
		RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	var integrationID string
	if err := pool.QueryRow(ctx, `INSERT INTO integrations (name, connector_id, protocol, endpoint, username, credential_ref, status, environment, tenant_id)
		VALUES ('database-query-governance-test', $1, 'mysql', 'mysql://db.example.test/app', 'reader', 'db-test-cred', 'ACTIVE', 'testing', 'platform')
		RETURNING id`, connectorID).Scan(&integrationID); err != nil {
		t.Fatal(err)
	}
	var resourceID string
	if err := pool.QueryRow(ctx, `INSERT INTO resources (resource_type, external_id, name, namespace, connector_id, integration_id, environment, status, tenant_id)
		VALUES ('database_table', 'table/app/user_token', 'user_token', 'app', $1, $2, 'testing', 'ACTIVE', 'platform')
		RETURNING id`, connectorID, integrationID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	return resourceID, connectorID
}

func insertDatabaseQueryPreview(t *testing.T, pool *pgxpool.Pool, resourceID, connectorID, previewHash, verdict string) {
	t.Helper()
	ctx := domain.WithTenantID(context.Background(), "platform")
	var actingProjectID string
	if err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE tenant_id='platform' AND is_default`).Scan(&actingProjectID); err != nil {
		t.Fatal(err)
	}
	parameters := map[string]any{"ast": map[string]any{"astVersion": 1}}
	if _, err := pool.Exec(ctx, `INSERT INTO executions
		(tenant_id, principal_id, acting_project_id, resource_id, action_id, parameters, parameter_hash, status, trace_id,
		 timeout_seconds, connector_id, result, executor_type)
		SELECT 'platform', $1, $2, $3, a.id, $4, $5, 'SUCCEEDED', 'query-preview-test',
			a.timeout_seconds, $6, $7, 'connector'
		FROM action_definitions a WHERE a.name='database.query.preview' AND a.version=1`,
		adminPrincipalID, actingProjectID, resourceID, parameters, domain.HashParameters(parameters), connectorID,
		map[string]any{"previewHash": previewHash, "verdict": verdict}); err != nil {
		t.Fatal(err)
	}
}
