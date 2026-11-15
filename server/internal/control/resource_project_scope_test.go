package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"gitee.com/pam-platform/warden/server/internal/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResourceProjectScopeModesAndTenantIsolation(t *testing.T) {
	pool := newTestDatabasePool(t)
	ctx := domain.WithTenantID(context.Background(), "platform")
	projects := postgres.NewProjectRepo(pool)
	var defaultProjectID, secondProjectID, connectorID, resourceID string
	if err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE tenant_id='platform' AND is_default`).Scan(&defaultProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('scope-second','scope test','platform') RETURNING id`).Scan(&secondProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,project_id,tenant_id)
		VALUES ('scope-connector','kubernetes','ACTIVE','testing',$1,'platform') RETURNING id`, defaultProjectID).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,environment,status,tenant_id)
		VALUES ('deployment','apps/scope-api','scope-api',$1,'testing','ACTIVE','platform') RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := projects.SetResourceProject(ctx, resourceID, secondProjectID, "", "legacy compatibility"); err != nil {
		t.Fatalf("legacy project assignment: %v", err)
	}
	legacyScope, err := projects.GetResourceScope(ctx, resourceID)
	if err != nil || legacyScope == nil || legacyScope.Mode != domain.ProjectScopeInherited || !scopeContainsString(legacyScope.ProjectIDs, secondProjectID) {
		t.Fatalf("legacy project assignment did not map to inherited scope: %#v err=%v", legacyScope, err)
	}
	if err := projects.ReplaceResourceConsumers(ctx, resourceID, []string{defaultProjectID}); err != nil {
		t.Fatalf("legacy consumer assignment: %v", err)
	}
	legacyScope, err = projects.GetResourceScope(ctx, resourceID)
	if err != nil || legacyScope == nil || !scopeContainsString(legacyScope.ProjectIDs, defaultProjectID) {
		t.Fatalf("legacy consumer assignment did not update resource bindings: %#v err=%v", legacyScope, err)
	}

	if err := projects.ReplaceResourceScope(ctx, resourceID, domain.ProjectScopeSelected, []string{defaultProjectID, secondProjectID}, ""); err != nil {
		t.Fatalf("select projects: %v", err)
	}
	scope, err := projects.GetResourceScope(ctx, resourceID)
	if err != nil || scope == nil || scope.Mode != domain.ProjectScopeSelected || len(scope.ProjectIDs) != 2 {
		t.Fatalf("selected scope = %#v err=%v", scope, err)
	}
	h := &Handler{projects: projects, resources: postgres.NewResourceRepo(pool)}
	if got, err := h.resolveActingProject(ctx, resourceID, secondProjectID); err != nil || got != secondProjectID {
		t.Fatalf("resolve selected project = %q err=%v", got, err)
	}
	if _, err := h.resolveActingProject(ctx, resourceID, ""); err == nil {
		t.Fatal("multi-project resource accepted an execution without actingProjectId")
	}
	if _, err := h.resolveActingProject(ctx, resourceID, "00000000-0000-4000-8000-000000000099"); err == nil {
		t.Fatal("resource accepted a project outside its scope")
	}

	if err := projects.ReplaceResourceScope(ctx, resourceID, domain.ProjectScopeAll, nil, ""); err != nil {
		t.Fatalf("set all-project scope: %v", err)
	}
	var futureProjectID string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('scope-future','created later','platform') RETURNING id`).Scan(&futureProjectID); err != nil {
		t.Fatal(err)
	}
	scope, err = projects.GetResourceScope(ctx, resourceID)
	if err != nil || scope == nil || !scopeContainsString(scope.ProjectIDs, futureProjectID) {
		t.Fatalf("all-project scope did not include future project: %#v err=%v", scope, err)
	}

	if err := projects.ReplaceResourceScope(ctx, resourceID, domain.ProjectScopeUnassigned, nil, ""); err != nil {
		t.Fatalf("set unassigned scope: %v", err)
	}
	if _, err := h.resolveActingProject(ctx, resourceID, defaultProjectID); err == nil {
		t.Fatal("unassigned resource was executable")
	}

	const otherTenant = "resource-scope-other"
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1)`, otherTenant); err != nil {
		t.Fatal(err)
	}
	var otherProjectID string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('other-project','cross tenant',$1) RETURNING id`, otherTenant).Scan(&otherProjectID); err != nil {
		t.Fatal(err)
	}
	if err := projects.ReplaceResourceScope(ctx, resourceID, domain.ProjectScopeSelected, []string{otherProjectID}, ""); !errors.Is(err, postgres.ErrInvalidProjectScope) {
		t.Fatalf("cross-tenant binding error = %v, want ErrInvalidProjectScope", err)
	}
	scope, err = projects.GetResourceScope(ctx, resourceID)
	if err != nil || scope == nil || scope.Mode != domain.ProjectScopeUnassigned {
		t.Fatalf("failed cross-tenant update was not rolled back: scope=%#v err=%v", scope, err)
	}
}

func TestExecutionPersistsActingProjectAcrossDecisionAndAudit(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")
	var defaultProjectID, secondProjectID, resourceID string
	if err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE tenant_id='platform' AND is_default`).Scan(&defaultProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects(name,description,tenant_id) VALUES ('execution-second','execution context','platform') RETURNING id`).Scan(&secondProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM resources WHERE resource_type='credential_vault' AND tenant_id='platform' LIMIT 1`).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := h.projects.ReplaceResourceScope(ctx, resourceID, domain.ProjectScopeSelected, []string{defaultProjectID, secondProjectID}, ""); err != nil {
		t.Fatal(err)
	}

	request := func(actingProjectID string) *executionResponse {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"resourceId": resourceID, "actingProjectId": actingProjectID,
			"externalContext": map[string]any{
				"source": "federated", "workspaceId": "workspace-scope", "projectId": "external-scope",
				"agentId": "agent-scope", "taskId": "task-scope", "originatorUserId": "user-scope",
			},
			"action": "credential.list", "actionVersion": 1,
			"parameters": map[string]any{}, "reason": "acting project persistence test",
		})
		if err != nil {
			t.Fatal(err)
		}
		resp := doAdminRequest(t, mux, http.MethodPost, "/api/v1/executions", string(body))
		parsed := parseExecutionResponse(t, resp)
		if actingProjectID == "" {
			if resp.Code != http.StatusForbidden {
				t.Fatalf("missing acting project status=%d body=%s", resp.Code, resp.Body.String())
			}
			return nil
		}
		if resp.Code != http.StatusOK {
			t.Fatalf("valid acting project status=%d body=%s", resp.Code, resp.Body.String())
		}
		return &parsed
	}

	request("")
	execution := request(secondProjectID)
	var executionProjectID, decisionProjectID string
	if err := pool.QueryRow(ctx, `SELECT acting_project_id::text FROM executions WHERE id=$1`, execution.ID).Scan(&executionProjectID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT decision.acting_project_id::text FROM policy_decisions decision
		JOIN executions execution ON execution.decision_id=decision.id WHERE execution.id=$1`, execution.ID).Scan(&decisionProjectID); err != nil {
		t.Fatal(err)
	}
	if executionProjectID != secondProjectID || decisionProjectID != secondProjectID {
		t.Fatalf("acting project persistence execution=%q decision=%q want=%q", executionProjectID, decisionProjectID, secondProjectID)
	}
	var externalContext map[string]any
	if err := pool.QueryRow(ctx, `SELECT external_context FROM executions WHERE id=$1`, execution.ID).Scan(&externalContext); err != nil {
		t.Fatal(err)
	}
	if externalContext["source"] != "federated" || externalContext["workspaceId"] != "workspace-scope" || externalContext["taskId"] != "task-scope" {
		t.Fatalf("external execution context = %#v", externalContext)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE execution_id=$1 AND acting_project_id=$2`, execution.ID, secondProjectID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount == 0 {
		t.Fatal("execution audit events did not retain acting project")
	}
	var auditContext map[string]any
	if err := pool.QueryRow(ctx, `SELECT metadata->'external_context' FROM audit_events WHERE execution_id=$1 ORDER BY created_at LIMIT 1`, execution.ID).Scan(&auditContext); err != nil {
		t.Fatal(err)
	}
	if auditContext["agentId"] != "agent-scope" || auditContext["originatorUserId"] != "user-scope" {
		t.Fatalf("execution audit external context = %#v", auditContext)
	}

	if err := h.writeAudit(ctx, "execution.dispatched", nil, nil, execution.ID, "acting-project-dispatch"); err != nil {
		t.Fatal(err)
	}
	assertExecutionAuditProject(t, pool, execution.ID, "execution.dispatched", secondProjectID)

	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id)
		VALUES ('acting-project-result','test','ACTIVE','testing','platform') RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	const dispatchToken = "00000000-0000-4000-8000-000000000123"
	if _, err := pool.Exec(ctx, `UPDATE executions SET status='DISPATCHED', executor_type='connector', connector_id=$2,
		dispatch_token=$3, dispatch_expires_at=now()+interval '5 minutes' WHERE id=$1`, execution.ID, connectorID, dispatchToken); err != nil {
		t.Fatal(err)
	}
	resultBody := bytes.NewBufferString(`{"dispatchToken":"` + dispatchToken + `","status":"SUCCEEDED","result":{"ok":true}}`)
	resultReq := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/"+connectorID+"/tasks/"+execution.ID+"/result", resultBody)
	resultReq = resultReq.WithContext(WithTenantContext(resultReq.Context(), "platform"))
	resultReq.SetPathValue("id", connectorID)
	resultReq.SetPathValue("executionId", execution.ID)
	resultResp := httptest.NewRecorder()
	h.SubmitTaskResult(resultResp, resultReq)
	if resultResp.Code != http.StatusOK {
		t.Fatalf("submit task result status=%d body=%s", resultResp.Code, resultResp.Body.String())
	}
	assertExecutionAuditProject(t, pool, execution.ID, "execution.completed", secondProjectID)

	deleted := doAdminRequest(t, mux, http.MethodDelete, "/api/v1/projects/"+secondProjectID, "")
	if deleted.Code != http.StatusConflict || !strings.Contains(deleted.Body.String(), "PROJECT_IN_USE") {
		t.Fatalf("delete project with audit history status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func assertExecutionAuditProject(t *testing.T, pool *pgxpool.Pool, executionID, action, projectID string) {
	t.Helper()
	ctx := domain.WithTenantID(context.Background(), "platform")
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events
		WHERE execution_id=$1 AND action=$2 AND acting_project_id=$3`, executionID, action, projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("audit action %s did not retain acting project %s", action, projectID)
	}
}

func scopeContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
