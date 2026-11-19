package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	dingcard "github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---
//
// Tenant-level group isolation: the DingTalk app is deployment-shared (like a
// single OIDC IdP), but every tenant binds its own approval group. Queues,
// decisions, cards, notifications and audit must stay inside the request's /
// group's tenant boundary.

type dingTalkIsolationFixture struct {
	pool        *pgxpool.Pool
	h           *Handler
	mux         *http.ServeMux
	mu          sync.Mutex
	cardCreates []map[string]any
	webhookHits int
}

func newDingTalkIsolationFixture(t *testing.T) *dingTalkIsolationFixture {
	t.Helper()
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	t.Setenv("WARDEN_CORS_ORIGIN", "https://warden.example.test")
	f := &dingTalkIsolationFixture{pool: pool, h: h, mux: mux}

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app-token":
			_, _ = w.Write([]byte(`{"accessToken":"app-token","expireIn":7200}`))
		case "/user-token":
			_, _ = w.Write([]byte(`{"accessToken":"user-token","corpId":"corp-id"}`))
		case "/me":
			_, _ = w.Write([]byte(`{"unionId":"union-1"}`))
		case "/union-user":
			_, _ = w.Write([]byte(`{"errcode":0,"result":{"userid":"ding-user-1"}}`))
		case "/user-detail":
			_, _ = w.Write([]byte(`{"errcode":0,"result":{"userid":"ding-user-1","name":"张三"}}`))
		case "/card-create":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			f.mu.Lock()
			f.cardCreates = append(f.cardCreates, payload)
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"result":{"deliverResults":[{"success":true}]}}`))
		case "/card-update":
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/webhook":
			f.mu.Lock()
			f.webhookHits++
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)

	app := &dingTalkCoolAppClient{
		appKey: "ding-app-key", appSecret: "secret-value-long-enough", http: fake.Client(),
		tokenEndpoint: fake.URL + "/app-token", userTokenEndpoint: fake.URL + "/user-token",
		currentUserEndpoint: fake.URL + "/me", unionUserEndpoint: fake.URL + "/union-user",
		userDetailEndpoint: fake.URL + "/user-detail",
	}
	h.dingTalkCoolApp = app
	h.dingTalkCard = &dingTalkCardClient{
		app: app, templateID: "tpl", openConversationID: "cid-platform", robotCode: "robot",
		http: fake.Client(), createEndpoint: fake.URL + "/card-create", updateEndpoint: fake.URL + "/card-update",
	}
	webhookURL, err := url.Parse(fake.URL + "/webhook")
	if err != nil {
		t.Fatal(err)
	}
	h.dingTalk = &dingTalkClient{webhook: webhookURL, http: fake.Client(), now: time.Now}

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ('acme','Acme'),('chunf','Chunf') ON CONFLICT (slug) DO NOTHING`); err != nil {
		t.Fatalf("seed tenants: %v", err)
	}
	for tenant, conversation := range map[string]string{"acme": "cid-acme", "chunf": "cid-chunf"} {
		channel := &domain.NotificationChannel{Type: dingTalkChannelType, Name: tenant + " 审批", Enabled: true,
			Events: append([]string(nil), allowedDingTalkEvents...), OpenConversationID: conversation}
		if err := h.notificationChannels.Upsert(domain.WithTenantID(ctx, tenant), channel); err != nil {
			t.Fatalf("seed %s channel: %v", tenant, err)
		}
	}
	return f
}

// seedTenantPendingApproval creates a member principal, connector, resource,
// WAITING_APPROVAL execution and its PENDING request inside one tenant.
func (f *dingTalkIsolationFixture) seedTenantPendingApproval(t *testing.T, tenant, trace string) (requestID, executionID string) {
	t.Helper()
	ctx := context.Background()
	var applicantID, connectorID, resourceID string
	if err := f.pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata) VALUES ('user',$1,'ACTIVE',$2,'{}') RETURNING id`, "applicant-"+trace, tenant).Scan(&applicantID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,version,capabilities,last_heartbeat,tenant_id)
		VALUES ($1,'kubernetes','ACTIVE','staging','1.0.0','["provider.request@v1"]'::jsonb,now(),$2) RETURNING id`, "conn-"+trace, tenant).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,namespace,connector_id,environment,status,tenant_id)
		VALUES ('deployment',$1,$2,'default',$3,'staging','ACTIVE',$4) RETURNING id`, "ext-"+trace, "res-"+trace, connectorID, tenant).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO executions(tenant_id,principal_id,resource_id,action_id,parameters,parameter_hash,status,trace_id,connector_id)
		VALUES ($1,$2,$3,'a0000000-0000-0000-0000-000000000001','{}'::jsonb,'iso-hash','WAITING_APPROVAL',$4,$5) RETURNING id`,
		tenant, applicantID, resourceID, trace, connectorID).Scan(&executionID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO access_requests(tenant_id,principal_id,resource_id,action_id,execution_id,parameters,parameter_hash,reason,status,expires_at)
		VALUES ($1,$2,$3,'a0000000-0000-0000-0000-000000000001',$4,'{}'::jsonb,'iso-hash','isolation test','PENDING',now()+interval '1 hour') RETURNING id`,
		tenant, applicantID, resourceID, executionID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	return requestID, executionID
}

func (f *dingTalkIsolationFixture) auth(t *testing.T, conversation string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"auth_code":"code-1","corp_id":"corp-id","open_conversation_id":%q}`, conversation)
	req := httptest.NewRequest(http.MethodPost, "https://warden.example.test/api/v1/dingtalk/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://warden.example.test")
	resp := httptest.NewRecorder()
	f.mux.ServeHTTP(resp, req)
	return resp
}

func dingTalkSessionCookie(t *testing.T, resp *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == dingTalkCookieName {
			return cookie
		}
	}
	t.Fatalf("no DingTalk session cookie in response: %s", resp.Body.String())
	return nil
}

func (f *dingTalkIsolationFixture) coolAppRequest(t *testing.T, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "https://warden.example.test"+path, nil)
	req.Header.Set("Origin", "https://warden.example.test")
	req.AddCookie(cookie)
	resp := httptest.NewRecorder()
	f.mux.ServeHTTP(resp, req)
	return resp
}

func TestDingTalkCoolAppTenantIsolation(t *testing.T) {
	f := newDingTalkIsolationFixture(t)
	acmeRequest, _ := f.seedTenantPendingApproval(t, "acme", "trace-iso-acme")
	chunfRequest, _ := f.seedTenantPendingApproval(t, "chunf", "trace-iso-chunf")

	// Login through acme's bound group.
	authResp := f.auth(t, "cid-acme")
	if authResp.Code != http.StatusOK {
		t.Fatalf("acme auth status=%d body=%s", authResp.Code, authResp.Body.String())
	}
	cookie := dingTalkSessionCookie(t, authResp)

	// The queue shows only acme's request.
	list := f.coolAppRequest(t, http.MethodGet, "/api/v1/dingtalk/access-requests", cookie)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), acmeRequest) || strings.Contains(list.Body.String(), chunfRequest) {
		t.Fatalf("queue leaked across tenants: %s", list.Body.String())
	}

	// Approving acme's request lands request/grant/audit in acme.
	approve := f.coolAppRequest(t, http.MethodPost, "/api/v1/dingtalk/access-requests/"+acmeRequest+"/approve", cookie)
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approve.Code, approve.Body.String())
	}
	var requestStatus, grantTenant, auditTenant string
	if err := f.pool.QueryRow(context.Background(), `SELECT status FROM access_requests WHERE id=$1`, acmeRequest).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT tenant_id FROM access_grants WHERE request_id=$1`, acmeRequest).Scan(&grantTenant); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT tenant_id FROM audit_events WHERE action='access_request.approved' AND target_id=$1`, acmeRequest).Scan(&auditTenant); err != nil {
		t.Fatal(err)
	}
	if requestStatus != "APPROVED" || grantTenant != "acme" || auditTenant != "acme" {
		t.Fatalf("request=%s grant tenant=%s audit tenant=%s, want APPROVED/acme/acme", requestStatus, grantTenant, auditTenant)
	}

	// acme's session cannot even see chunf's request by ID.
	cross := f.coolAppRequest(t, http.MethodPost, "/api/v1/dingtalk/access-requests/"+chunfRequest+"/approve", cookie)
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant approve status=%d, want 404, body=%s", cross.Code, cross.Body.String())
	}

	// Unbound groups fail closed; the deployment-level group maps to platform.
	unbound := f.auth(t, "cid-unknown")
	if unbound.Code != http.StatusConflict || !strings.Contains(unbound.Body.String(), "DINGTALK_GROUP_NOT_BOUND") {
		t.Fatalf("unbound group status=%d body=%s", unbound.Code, unbound.Body.String())
	}
	platformAuth := f.auth(t, "cid-platform")
	if platformAuth.Code != http.StatusOK {
		t.Fatalf("platform env group auth status=%d body=%s", platformAuth.Code, platformAuth.Body.String())
	}
}

func TestDingTalkCardCallbackTenantIsolation(t *testing.T) {
	f := newDingTalkIsolationFixture(t)
	acmeRequest, _ := f.seedTenantPendingApproval(t, "acme", "trace-card-acme")
	chunfRequest, _ := f.seedTenantPendingApproval(t, "chunf", "trace-card-chunf")

	callback := func(requestID, space string) (*dingcard.CardResponse, error) {
		request := &dingcard.CardRequest{
			OutTrackId: dingTalkCardTrackID(requestID), SpaceId: space, UserId: "ding-user-1",
			CardActionData: dingcard.PrivateCardActionData{CardPrivateData: dingcard.CardPrivateData{ActionIdList: []string{"approve"}}},
		}
		return f.h.handleDingTalkCardCallback(context.Background(), request)
	}

	// acme's card decided from acme's group: audit lands in acme.
	if _, err := callback(acmeRequest, "cid-acme"); err != nil {
		t.Fatalf("acme callback failed: %v", err)
	}
	var auditTenant string
	if err := f.pool.QueryRow(context.Background(), `SELECT tenant_id FROM audit_events WHERE action='access_request.approved' AND target_id=$1`, acmeRequest).Scan(&auditTenant); err != nil {
		t.Fatal(err)
	}
	if auditTenant != "acme" {
		t.Fatalf("card audit tenant=%s, want acme", auditTenant)
	}

	// The same request replayed from chunf's group is rejected.
	if _, err := callback(chunfRequest, "cid-acme"); err == nil {
		t.Fatal("cross-group card replay was accepted")
	}
	// chunf's own group works and stays in chunf.
	if _, err := callback(chunfRequest, "cid-chunf"); err != nil {
		t.Fatalf("chunf callback failed: %v", err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT tenant_id FROM audit_events WHERE action='access_request.approved' AND target_id=$1`, chunfRequest).Scan(&auditTenant); err != nil {
		t.Fatal(err)
	}
	if auditTenant != "chunf" {
		t.Fatalf("chunf card audit tenant=%s, want chunf", auditTenant)
	}

	// Test cards: tenant-carrying track ID + matching group passes; wrong
	// group and legacy tenant-less IDs are rejected.
	if _, err := callback("channel-test-acme-1730000000000000000", "cid-acme"); err != nil {
		t.Fatalf("tenant test card rejected: %v", err)
	}
	if _, err := callback("channel-test-acme-1730000000000000000", "cid-chunf"); err == nil {
		t.Fatal("test card from wrong group accepted")
	}
	if _, err := callback("channel-test-1730000000000000000", "cid-acme"); err == nil {
		t.Fatal("legacy tenant-less test card accepted")
	}
}

func TestDingTalkLegacySessionWithoutTenantRejected(t *testing.T) {
	f := newDingTalkIsolationFixture(t)
	now := time.Now()
	claims := dingTalkClaims{UserID: "ding-user-1", PrincipalID: adminPrincipalID, CorpID: "corp-id",
		RegisteredClaims: jwt.RegisteredClaims{Subject: adminPrincipalID, Issuer: jwtIssuer, Audience: jwt.ClaimStrings{dingTalkAudience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(f.h.jwtSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	resp := f.coolAppRequest(t, http.MethodGet, "/api/v1/dingtalk/access-requests", &http.Cookie{Name: dingTalkCookieName, Value: signed})
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("legacy session status=%d, want 401, body=%s", resp.Code, resp.Body.String())
	}
}

func TestDingTalkChannelGroupBindingConflicts(t *testing.T) {
	f := newDingTalkIsolationFixture(t)

	// platform tries to bind acme's group -> partial unique index rejects.
	resp := doAdminRequest(t, f.mux, http.MethodPut, "/api/v1/notification-channels/dingtalk",
		`{"name":"platform 审批","enabled":false,"events":["access_request.created"],"open_conversation_id":"cid-acme"}`)
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "DINGTALK_GROUP_BOUND") {
		t.Fatalf("duplicate binding status=%d body=%s", resp.Code, resp.Body.String())
	}

	// acme's tenant admin tries to bind the deployment-level platform group.
	hash, err := hashPassword("tenant-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash)
		VALUES ('user','acme-admin','ACTIVE','acme','{"role":"tenant_admin"}',$1)`, hash); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"acme-admin","password":"tenant-password-123","tenant":"acme"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "https://warden.example.test")
	f.mux.ServeHTTP(login, loginReq)
	if login.Code != http.StatusOK {
		t.Fatalf("acme admin login status=%d body=%s", login.Code, login.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == "warden_session" {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("login issued no session cookie")
	}
	put := httptest.NewRequest(http.MethodPut, "https://warden.example.test/api/v1/notification-channels/dingtalk",
		strings.NewReader(`{"name":"acme 审批","enabled":true,"events":["access_request.created"],"open_conversation_id":"cid-platform"}`))
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("Origin", "https://warden.example.test")
	put.AddCookie(sessionCookie)
	conflict := httptest.NewRecorder()
	f.mux.ServeHTTP(conflict, put)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "DINGTALK_GROUP_CONFLICT") {
		t.Fatalf("platform-group binding status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestDingTalkDeliveryTenantIsolation(t *testing.T) {
	f := newDingTalkIsolationFixture(t)
	acmeRequest, _ := f.seedTenantPendingApproval(t, "acme", "trace-deliver-acme")
	var request domain.AccessRequest
	if err := f.pool.QueryRow(context.Background(), `SELECT id, tenant_id, principal_id, resource_id, action_id, parameters, parameter_hash, status FROM access_requests WHERE id=$1`, acmeRequest).
		Scan(&request.ID, &request.TenantID, &request.PrincipalID, &request.ResourceID, &request.ActionID, &request.Parameters, &request.ParameterHash, &request.Status); err != nil {
		t.Fatal(err)
	}

	// acme delivery: card goes to acme's group, the shared webhook is untouched.
	if err := f.h.deliverApprovalNotification(domain.WithTenantID(context.Background(), "acme"), request, "access_request.created", ""); err != nil {
		t.Fatalf("acme delivery: %v", err)
	}
	f.mu.Lock()
	creates, webhookHits := len(f.cardCreates), f.webhookHits
	var space string
	if creates > 0 {
		space, _ = f.cardCreates[creates-1]["openSpaceId"].(string)
	}
	f.mu.Unlock()
	if creates != 1 || space != "dtv1.card//im_group.cid-acme" || webhookHits != 0 {
		t.Fatalf("acme delivery creates=%d space=%q webhookHits=%d", creates, space, webhookHits)
	}

	// platform delivery may use the deployment-level webhook. Migration 029
	// seeds the platform channel disabled, so enable it first like an admin would.
	platformChannel := &domain.NotificationChannel{Type: dingTalkChannelType, Name: "platform 审批", Enabled: true,
		Events: append([]string(nil), allowedDingTalkEvents...)}
	if err := f.h.notificationChannels.Upsert(domain.WithTenantID(context.Background(), "platform"), platformChannel); err != nil {
		t.Fatal(err)
	}
	platformRequest := request
	platformRequest.TenantID = "platform"
	if err := f.h.deliverApprovalNotification(domain.WithTenantID(context.Background(), "platform"), platformRequest, "access_request.created", ""); err != nil {
		t.Fatalf("platform delivery: %v", err)
	}
	f.mu.Lock()
	webhookHits = f.webhookHits
	f.mu.Unlock()
	if webhookHits != 1 {
		t.Fatalf("platform webhook hits=%d, want 1", webhookHits)
	}
}