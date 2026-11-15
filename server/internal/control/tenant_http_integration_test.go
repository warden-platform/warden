package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestTenantAdminLoginAndHTTPIsolation(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()
	const tenant = "http-isolation-acme"
	_, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1)`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_quotas(tenant_id) VALUES ($1)`, tenant); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM admin_sessions WHERE principal_id IN (SELECT id FROM principals WHERE tenant_id=$1)`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM cluster_pairings WHERE operator_id='tenant-host-machine'`)
		_, _ = pool.Exec(ctx, `DELETE FROM connectors WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM principals WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM tenant_quotas WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE slug=$1`, tenant)
	}()
	hash, err := hashPassword("tenant-password-123")
	if err != nil {
		t.Fatal(err)
	}
	var principalID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash) VALUES ('user','acme-admin','ACTIVE',$1,'{"role":"tenant_admin"}',$2) RETURNING id`, tenant, hash).Scan(&principalID); err != nil {
		t.Fatal(err)
	}

	loginBody := `{"username":"acme-admin","password":"tenant-password-123","tenant":"http-isolation-acme"}`
	login := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(loginBody))
	h.Login(login, req)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()
	if len(cookie) == 0 || cookie[0].Name != "pam_session" {
		t.Fatalf("login did not issue session cookie: %#v", cookie)
	}

	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie[0])
	meReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(me, meReq)
	if me.Code != http.StatusOK {
		t.Fatalf("auth/me status=%d body=%s", me.Code, me.Body.String())
	}
	var current struct {
		Role            string        `json:"role"`
		Scopes          []string      `json:"scopes"`
		IsPlatformAdmin bool          `json:"is_platform_admin"`
		Tenant          domain.Tenant `json:"tenant"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode auth/me: %v", err)
	}
	if current.Tenant.Slug != tenant || current.Role != "tenant_admin" || current.IsPlatformAdmin || !hasScopeValue(current.Scopes, "pam:tenant:admin") {
		t.Fatalf("unexpected auth/me identity: %#v", current)
	}

	principals := httptest.NewRecorder()
	pReq := httptest.NewRequest(http.MethodGet, "/api/v1/principals", nil)
	pReq.AddCookie(cookie[0])
	pReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(principals, pReq)
	if principals.Code != http.StatusOK {
		t.Fatalf("tenant principals status=%d body=%s", principals.Code, principals.Body.String())
	}
	if !containsJSONName(principals.Body.Bytes(), "acme-admin") {
		t.Fatalf("tenant principal missing: %s", principals.Body.String())
	}
	// Seed one resource in each tenant with deliberately similar names. The
	// authenticated resource list must only contain the current tenant's row.
	var acmeConnector, platformConnector string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id) VALUES ('http-acme-connector','kubernetes','ACTIVE','testing',$1) RETURNING id`, tenant).Scan(&acmeConnector); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,tenant_id) VALUES ('http-platform-connector','kubernetes','ACTIVE','testing','platform') RETURNING id`).Scan(&platformConnector); err != nil {
		t.Fatal(err)
	}
	var platformIntegration string
	if err := pool.QueryRow(ctx, `INSERT INTO integrations(connector_id,protocol,name,endpoint,credential_ref,status,tenant_id)
		VALUES ($1,'jenkins','http-platform-integration','https://jenkins.example.test','pam:test','ACTIVE','platform') RETURNING id`, platformConnector).Scan(&platformIntegration); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM integrations WHERE id=$1`, platformIntegration)
	crossDeleteIntegration := httptest.NewRecorder()
	crossDeleteIntegrationReq := httptest.NewRequest(http.MethodDelete, "/api/v1/connectors/"+platformConnector+"/integrations/"+platformIntegration, nil)
	crossDeleteIntegrationReq.AddCookie(cookie[0])
	crossDeleteIntegrationReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(crossDeleteIntegration, crossDeleteIntegrationReq)
	if crossDeleteIntegration.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant integration delete status=%d body=%s", crossDeleteIntegration.Code, crossDeleteIntegration.Body.String())
	}

	var platformDefaultProject, platformRule string
	if err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE tenant_id='platform' AND is_default`).Scan(&platformDefaultProject); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO project_namespace_rules(project_id,connector_id,namespace)
		VALUES ($1,$2,'http-cross-tenant') RETURNING id`, platformDefaultProject, platformConnector).Scan(&platformRule); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM project_namespace_rules WHERE id=$1`, platformRule)
	crossDeleteRule := httptest.NewRecorder()
	crossDeleteRuleReq := httptest.NewRequest(http.MethodDelete, "/api/v1/project-namespace-rules/"+platformRule, nil)
	crossDeleteRuleReq.AddCookie(cookie[0])
	crossDeleteRuleReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(crossDeleteRule, crossDeleteRuleReq)
	if crossDeleteRule.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant namespace rule delete status=%d body=%s", crossDeleteRule.Code, crossDeleteRule.Body.String())
	}
	_, err = pool.Exec(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,status,tenant_id) VALUES ('namespace','http-acme-resource',$1,$2,'ACTIVE',$3),('namespace','http-platform-resource',$4,$5,'ACTIVE','platform')`, "http-acme-resource", acmeConnector, tenant, "http-platform-resource", platformConnector)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM resources WHERE connector_id IN ($1,$2)`, acmeConnector, platformConnector)
	defer pool.Exec(ctx, `DELETE FROM connectors WHERE id IN ($1,$2)`, acmeConnector, platformConnector)
	resources := httptest.NewRecorder()
	rReq := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
	rReq.AddCookie(cookie[0])
	mux.ServeHTTP(resources, rReq)
	if resources.Code != http.StatusOK {
		t.Fatalf("tenant resources status=%d body=%s", resources.Code, resources.Body.String())
	}
	if !containsJSONName(resources.Body.Bytes(), "http-acme-resource") || containsJSONName(resources.Body.Bytes(), "http-platform-resource") {
		t.Fatalf("resource tenant isolation failed: %s", resources.Body.String())
	}
	connectors := httptest.NewRecorder()
	cReq := httptest.NewRequest(http.MethodGet, "/api/v1/connectors", nil)
	cReq.AddCookie(cookie[0])
	mux.ServeHTTP(connectors, cReq)
	if connectors.Code != http.StatusOK {
		t.Fatalf("tenant connectors status=%d body=%s", connectors.Code, connectors.Body.String())
	}
	if !containsJSONName(connectors.Body.Bytes(), "http-acme-connector") || containsJSONName(connectors.Body.Bytes(), "http-platform-connector") {
		t.Fatalf("connector tenant isolation failed: %s", connectors.Body.String())
	}
	policies := httptest.NewRecorder()
	polReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy-rules", nil)
	polReq.AddCookie(cookie[0])
	mux.ServeHTTP(policies, polReq)
	if policies.Code != http.StatusOK {
		t.Fatalf("tenant policy rules status=%d body=%s", policies.Code, policies.Body.String())
	}

	var platformCapabilityID string
	if err := pool.QueryRow(ctx, `SELECT id FROM provider_capabilities WHERE tenant_id='platform' AND status='ACTIVE' ORDER BY created_at LIMIT 1`).Scan(&platformCapabilityID); err != nil {
		t.Fatal(err)
	}
	foreignUpdate := httptest.NewRecorder()
	foreignUpdateReq := httptest.NewRequest(http.MethodPut, "/api/v1/provider-capabilities/"+platformCapabilityID, bytes.NewBufferString(`{
		"name":"cross-tenant-attempt","version":1,"resourceKinds":["jenkins_job"],"protocols":["jenkins"],
		"methods":["GET"],"pathPatterns":["/job/{job}/api/json"]}`))
	foreignUpdateReq.AddCookie(cookie[0])
	foreignUpdateReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(foreignUpdate, foreignUpdateReq)
	if foreignUpdate.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant capability update status=%d body=%s", foreignUpdate.Code, foreignUpdate.Body.String())
	}
	foreignDelete := httptest.NewRecorder()
	foreignDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/provider-capabilities/"+platformCapabilityID, nil)
	foreignDeleteReq.AddCookie(cookie[0])
	foreignDeleteReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(foreignDelete, foreignDeleteReq)
	if foreignDelete.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant capability delete status=%d body=%s", foreignDelete.Code, foreignDelete.Body.String())
	}
	var platformCapabilityStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM provider_capabilities WHERE id=$1`, platformCapabilityID).Scan(&platformCapabilityStatus); err != nil || platformCapabilityStatus != "ACTIVE" {
		t.Fatalf("platform capability changed across tenant: status=%q err=%v", platformCapabilityStatus, err)
	}

	pairingCode := "tenant-host-pairing"
	encryptedTaskKey, err := h.credentialCipher.Encrypt([]byte("tenant-host-task-key-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cluster_pairings
		(code_hash,bootstrap_token_hash,operator_id,cluster_uid,connector_type,available_services,machine_metadata,task_key_ciphertext,expires_at)
		VALUES ($1,$2,'tenant-host-machine','tenant-host-machine','macos',$3,$4,$5,$6)`,
		hashPairingCode(pairingCode), hashBootstrapToken("tenant-host-bootstrap-token"), []string{"uname"},
		map[string]any{"hostname": "tenant-host", "machineId": "tenant-host-machine", "platform": "macos"}, encryptedTaskKey, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	confirmHost := httptest.NewRecorder()
	confirmHostReq := httptest.NewRequest(http.MethodPost, "/api/v1/host-pairings/confirm", bytes.NewBufferString(`{
		"Code":"tenant-host-pairing","Platform":"macos","Name":"tenant-host-connector","Environment":"testing",
		"allowedCommands":["uname"]}`))
	confirmHostReq.AddCookie(cookie[0])
	confirmHostReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(confirmHost, confirmHostReq)
	if confirmHost.Code != http.StatusOK {
		t.Fatalf("tenant host pairing status=%d body=%s", confirmHost.Code, confirmHost.Body.String())
	}
	var pairedTenant string
	if err := pool.QueryRow(ctx, `SELECT tenant_id FROM connectors WHERE name='tenant-host-connector'`).Scan(&pairedTenant); err != nil || pairedTenant != tenant {
		t.Fatalf("paired connector tenant=%q err=%v", pairedTenant, err)
	}

	platform := httptest.NewRecorder()
	platformReq := httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil)
	platformReq.Header.Set("Authorization", "Bearer "+testAdminAPIKey)
	mux.ServeHTTP(platform, platformReq)
	if platform.Code != http.StatusOK || !containsJSONName(platform.Body.Bytes(), tenant) {
		t.Fatalf("platform tenant management status=%d body=%s", platform.Code, platform.Body.String())
	}

	logout := httptest.NewRecorder()
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(cookie[0])
	logoutReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(logout, logoutReq)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	_ = principalID
}

func TestPlatformBootstrapTenantCreatesCompleteBoundary(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()
	const tenant = "http-bootstrap-acme"
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM principals WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM tenant_quotas WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE slug=$1`, tenant)
	}()

	body := `{"slug":"http-bootstrap-acme","name":"Bootstrap Acme","adminName":"bootstrap-admin","adminPassword":"bootstrap-password","quota":{"maxPrincipals":25,"maxConnectors":5,"maxConcurrentTasks":7,"maxRequestsPerMinute":90,"auditRetentionDays":180}}`
	created := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants/bootstrap", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+testAdminAPIKey)
	mux.ServeHTTP(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", created.Code, created.Body.String())
	}

	detail := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/tenants/"+tenant, nil)
	detailReq.Header.Set("Authorization", "Bearer "+testAdminAPIKey)
	mux.ServeHTTP(detail, detailReq)
	if detail.Code != http.StatusOK {
		t.Fatalf("tenant detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var summary domain.TenantSummary
	if err := json.Unmarshal(detail.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode tenant detail: %v", err)
	}
	if summary.Slug != tenant || summary.Quota.MaxPrincipals != 25 || summary.Usage.Administrators != 1 || summary.Usage.Projects != 1 {
		t.Fatalf("unexpected tenant detail: %#v", summary)
	}

	admins := httptest.NewRecorder()
	adminsReq := httptest.NewRequest(http.MethodGet, "/api/v1/tenants/"+tenant+"/admins", nil)
	adminsReq.Header.Set("Authorization", "Bearer "+testAdminAPIKey)
	mux.ServeHTTP(admins, adminsReq)
	if admins.Code != http.StatusOK || !containsJSONName(admins.Body.Bytes(), "bootstrap-admin") {
		t.Fatalf("tenant admins status=%d body=%s", admins.Code, admins.Body.String())
	}
}

func containsJSONName(data []byte, name string) bool {
	return string(data) != "" && string(data) != name && (len(data) > 0 && contains(data, name))
}
func contains(data []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(data); i++ {
		if string(data[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
