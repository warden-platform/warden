package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---
//
// Visibility ladder regression: GetExecution / CancelExecution must agree with
// ListExecutions — platform admin cross-tenant (via the tenant switch header),
// tenant admin whole-org, member own runs only. Previously detail/cancel only
// accepted the platform admin or the executor, so tenant admins saw member
// executions in the list but got 404 by ID and could not cancel them.

type visibilityFixture struct {
	pool *pgxpool.Pool
	mux  *http.ServeMux
}

func newVisibilityFixture(t *testing.T) *visibilityFixture {
	t.Helper()
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ('vis-acme','Vis Acme'),('vis-chunf','Vis Chunf') ON CONFLICT (slug) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	return &visibilityFixture{pool: pool, mux: mux}
}

func (f *visibilityFixture) seedUser(t *testing.T, tenant, name, role string) string {
	t.Helper()
	hash, err := hashPassword("visibility-password-123")
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash)
		VALUES ('user',$1,'ACTIVE',$2,$3,$4) RETURNING id`, name, tenant, `{"role":"`+role+`"}`, hash).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *visibilityFixture) seedExecution(t *testing.T, tenant, principalID, trace string) string {
	t.Helper()
	ctx := context.Background()
	var connectorID, resourceID, executionID string
	if err := f.pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,version,capabilities,last_heartbeat,tenant_id)
		VALUES ($1,'kubernetes','ACTIVE','staging','1.0.0','["provider.request@v1"]'::jsonb,now(),$2) RETURNING id`, "conn-"+trace, tenant).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,namespace,connector_id,environment,status,tenant_id)
		VALUES ('deployment',$1,$2,'default',$3,'staging','ACTIVE',$4) RETURNING id`, "ext-"+trace, "res-"+trace, connectorID, tenant).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO executions(tenant_id,principal_id,resource_id,action_id,parameters,parameter_hash,status,trace_id,connector_id)
		VALUES ($1,$2,$3,'a0000000-0000-0000-0000-000000000001','{}'::jsonb,'vis-hash','QUEUED',$4,$5) RETURNING id`,
		tenant, principalID, resourceID, trace, connectorID).Scan(&executionID); err != nil {
		t.Fatal(err)
	}
	return executionID
}

func (f *visibilityFixture) login(t *testing.T, username, tenant string) *http.Cookie {
	t.Helper()
	body := `{"username":"` + username + `","password":"visibility-password-123","tenant":"` + tenant + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4173")
	resp := httptest.NewRecorder()
	f.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login %s@%s status=%d body=%s", username, tenant, resp.Code, resp.Body.String())
	}
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == "warden_session" {
			return cookie
		}
	}
	t.Fatal("login issued no session cookie")
	return nil
}

func (f *visibilityFixture) request(t *testing.T, method, path string, cookie *http.Cookie, tenantSwitch string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Origin", "http://localhost:4173")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if tenantSwitch != "" {
		req.Header.Set("X-Warden-Tenant", tenantSwitch)
	}
	resp := httptest.NewRecorder()
	f.mux.ServeHTTP(resp, req)
	return resp
}

func TestExecutionVisibilityLadder(t *testing.T) {
	f := newVisibilityFixture(t)
	acmeAdmin := f.seedUser(t, "vis-acme", "vis-admin", "tenant_admin")
	memberA := f.seedUser(t, "vis-acme", "vis-member-a", "user")
	memberB := f.seedUser(t, "vis-acme", "vis-member-b", "user")
	chunfMember := f.seedUser(t, "vis-chunf", "vis-chunf-member", "user")
	_ = acmeAdmin

	execA := f.seedExecution(t, "vis-acme", memberA, "trace-vis-a")
	execB := f.seedExecution(t, "vis-acme", memberB, "trace-vis-b")
	execChunf := f.seedExecution(t, "vis-chunf", chunfMember, "trace-vis-chunf")

	adminCookie := f.login(t, "vis-admin", "vis-acme")
	memberCookie := f.login(t, "vis-member-a", "vis-acme")

	// Tenant admin: whole-org detail access, including member and agent runs.
	for _, execID := range []string{execA, execB} {
		if resp := f.request(t, http.MethodGet, "/api/v1/executions/"+execID, adminCookie, ""); resp.Code != http.StatusOK {
			t.Fatalf("tenant admin GET %s status=%d body=%s", execID, resp.Code, resp.Body.String())
		}
	}
	// Tenant admin can cancel a member's queued execution.
	if resp := f.request(t, http.MethodPost, "/api/v1/executions/"+execB+"/cancel", adminCookie, ""); resp.Code != http.StatusOK {
		t.Fatalf("tenant admin cancel status=%d body=%s", resp.Code, resp.Body.String())
	}
	var status string
	if err := f.pool.QueryRow(context.Background(), `SELECT status FROM executions WHERE id=$1`, execB).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.ExecCancelled) {
		t.Fatalf("cancelled execution status=%s, want CANCELLED", status)
	}

	// Member: own run visible, other members' runs invisible and uncancellable.
	if resp := f.request(t, http.MethodGet, "/api/v1/executions/"+execA, memberCookie, ""); resp.Code != http.StatusOK {
		t.Fatalf("member GET own status=%d body=%s", resp.Code, resp.Body.String())
	}
	if resp := f.request(t, http.MethodGet, "/api/v1/executions/"+execB, memberCookie, ""); resp.Code != http.StatusNotFound {
		t.Fatalf("member GET other's status=%d, want 404", resp.Code)
	}
	if resp := f.request(t, http.MethodPost, "/api/v1/executions/"+execA+"/cancel", memberCookie, ""); resp.Code != http.StatusOK {
		t.Fatalf("member cancel own status=%d body=%s", resp.Code, resp.Body.String())
	}

	// Tenant boundary: acme admin cannot reach chunf's execution by ID.
	if resp := f.request(t, http.MethodGet, "/api/v1/executions/"+execChunf, adminCookie, ""); resp.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant GET status=%d, want 404", resp.Code)
	}

	// Platform admin via the tenant switch header sees the organization's runs.
	resp := doAdminRequest(t, f.mux, http.MethodGet, "/api/v1/executions/"+execA, "")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("platform admin without switch status=%d, want 404 (tenant-scoped lookup)", resp.Code)
	}
	switched := httptest.NewRequest(http.MethodGet, "/api/v1/executions/"+execA, nil)
	switched.Header.Set("Authorization", "Bearer "+testAdminAPIKey)
	switched.Header.Set("X-Warden-Tenant", "vis-acme")
	switchedResp := httptest.NewRecorder()
	f.mux.ServeHTTP(switchedResp, switched)
	if switchedResp.Code != http.StatusOK {
		t.Fatalf("platform admin with switch status=%d body=%s", switchedResp.Code, switchedResp.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(switchedResp.Body.Bytes(), &body); err != nil || body.ID != execA {
		t.Fatalf("switched detail body mismatch: %s", switchedResp.Body.String())
	}
}