package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestExternalDelegationExchangeBindsContextAndProject(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")
	var projectID string
	if err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE tenant_id='platform' AND is_default`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	var agentID, userID, keyID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata)
		VALUES ('agent_instance','delegation-agent','ACTIVE','platform','{}') RETURNING id`).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata)
		VALUES ('user','delegation-user','ACTIVE','platform','{}') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO principal_api_keys
		(principal_id,name,key_prefix,key_hash,status,purpose,scopes)
		VALUES ($1,'delegation-parent','warden_agk_test',decode(repeat('00',32),'hex'),'ACTIVE','AGENT',$2)
		RETURNING id`, agentID, defaultAgentScopes).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	var agentBindingID, userBindingID string
	if err := pool.QueryRow(ctx, `INSERT INTO external_identity_bindings
		(tenant_id,source,workspace_id,external_project_id,identity_type,external_id,principal_id,project_id,status)
		VALUES ('platform','federated','delegation-workspace','external-project','AGENT','external-agent',$1,$2,'ACTIVE') RETURNING id`, agentID, projectID).Scan(&agentBindingID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO external_identity_bindings
		(tenant_id,source,workspace_id,external_project_id,identity_type,external_id,principal_id,status)
		VALUES ('platform','federated','delegation-workspace','external-project','USER','external-user',$1,'ACTIVE') RETURNING id`, userID).Scan(&userBindingID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM external_identity_bindings WHERE id IN ($1,$2)`, agentBindingID, userBindingID)
		_, _ = pool.Exec(ctx, `DELETE FROM principal_api_keys WHERE id=$1`, keyID)
		_, _ = pool.Exec(ctx, `DELETE FROM principals WHERE id IN ($1,$2)`, agentID, userID)
	}()
	identity := &AuthenticatedIdentity{Principal: &domain.Principal{ID: agentID, Kind: domain.PrincipalKindAgentInstance, Status: domain.PrincipalStatusActive, TenantID: "platform"},
		TenantID: "platform", CredentialID: keyID, Scopes: defaultAgentScopes, Kind: "AGENT"}
	requestBody := `{"externalContext":{"source":"federated","workspaceId":"delegation-workspace","projectId":"external-project","agentId":"external-agent","taskId":"task-1","originatorUserId":"external-user","accountableUserId":"external-user","originatorSource":"human"},"ttlSeconds":120}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external-delegations/exchange", strings.NewReader(requestBody))
	req = req.WithContext(WithTenantContext(context.WithValue(req.Context(), identityContextKey{}, identity), "platform"))
	resp := httptest.NewRecorder()
	h.ExchangeDelegationToken(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("exchange status=%d body=%s", resp.Code, resp.Body.String())
	}
	var exchanged struct {
		Token           string `json:"delegationToken"`
		TokenID         string `json:"tokenId"`
		ActingProjectID string `json:"actingProjectId"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &exchanged); err != nil {
		t.Fatal(err)
	}
	if exchanged.Token == "" || exchanged.TokenID == "" || exchanged.ActingProjectID != projectID {
		t.Fatalf("unexpected exchange response: %#v", exchanged)
	}
	authenticated, err := h.authenticate(ctx, exchanged.Token, "127.0.0.1:1234")
	if err != nil || authenticated == nil {
		t.Fatalf("delegation authentication failed: %v", err)
	}
	if authenticated.DelegationProjectID != projectID || authenticated.DelegationExternalContext["taskId"] != "task-1" {
		t.Fatalf("delegation identity = %#v", authenticated)
	}
	var resourceID string
	if err := pool.QueryRow(ctx, `SELECT id FROM resources WHERE tenant_id='platform' AND resource_type='credential_vault' LIMIT 1`).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	executionBody := `{"resourceId":"` + resourceID + `","action":"credential.list","actionVersion":1,"parameters":{},"reason":"delegation integration test"}`
	executionReq := httptest.NewRequest(http.MethodPost, "/api/v1/executions", strings.NewReader(executionBody))
	executionReq.Header.Set("Authorization", "Bearer "+exchanged.Token)
	executionResp := httptest.NewRecorder()
	mux.ServeHTTP(executionResp, executionReq)
	if executionResp.Code != http.StatusOK {
		t.Fatalf("delegated execution status=%d body=%s", executionResp.Code, executionResp.Body.String())
	}
	var execution struct {
		ID              string         `json:"id"`
		ActingProjectID string         `json:"actingProjectId"`
		ExternalContext map[string]any `json:"externalContext"`
	}
	if err := json.Unmarshal(executionResp.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	if execution.ActingProjectID != projectID || execution.ExternalContext["agentId"] != "external-agent" {
		t.Fatalf("delegated execution context = %#v", execution)
	}
	wrongProjectReq := httptest.NewRequest(http.MethodPost, "/api/v1/executions", strings.NewReader(
		`{"resourceId":"`+resourceID+`","actingProjectId":"00000000-0000-4000-8000-000000000099","action":"credential.list","actionVersion":1,"parameters":{}}`))
	wrongProjectReq.Header.Set("Authorization", "Bearer "+exchanged.Token)
	wrongProjectResp := httptest.NewRecorder()
	mux.ServeHTTP(wrongProjectResp, wrongProjectReq)
	if wrongProjectResp.Code != http.StatusForbidden || !strings.Contains(wrongProjectResp.Body.String(), "DELEGATION_PROJECT_MISMATCH") {
		t.Fatalf("delegation project switch status=%d body=%s", wrongProjectResp.Code, wrongProjectResp.Body.String())
	}
	if _, err := h.externalIdentities.Revoke(ctx, agentBindingID, ""); err != nil {
		t.Fatal(err)
	}
	if afterRevoke, authErr := h.authenticate(ctx, exchanged.Token, "127.0.0.1:1234"); authErr != nil || afterRevoke != nil {
		t.Fatalf("delegation token remained usable after binding revoke: identity=%#v err=%v", afterRevoke, authErr)
	}
}
