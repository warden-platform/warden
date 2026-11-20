package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
)

// TestRLSAppRoleHTTPFlows 以真实 warden_app（主池）+ warden_privileged（旁路池）
// 驱动完整 Handler，验证 RLS 启用后关键 HTTP 流程不被误伤：租户管理员登录
// （GetByName 租户Scoped + UpdateLastLogin + 会话审计）、auth/me、超管跨租户
// ListAccounts（特权池）。control 层直连 SQL 旁路若漏路由会在此 fail-closed。
func TestRLSAppRoleHTTPFlows(t *testing.T) {
	ownerPool := newTestDatabasePool(t)
	ctx := context.Background()

	rolesSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "db", "roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, string(rolesSQL)); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, `ALTER ROLE warden_app LOGIN PASSWORD 'testapp'; ALTER ROLE warden_privileged LOGIN PASSWORD 'testpriv';`); err != nil {
		t.Fatal(err)
	}

	ownerURL := os.Getenv("WARDEN_TEST_DATABASE_URL")
	appDSN := rlsRoleURL(t, ownerURL, "warden_app", "testapp")
	privDSN := rlsRoleURL(t, ownerURL, "warden_privileged", "testpriv")
	t.Setenv("WARDEN_DATABASE_URL", appDSN)
	t.Setenv("WARDEN_PRIVILEGED_DATABASE_URL", privDSN)
	t.Setenv("WARDEN_TASK_SIGNING_KEY", "task-signing-key-0123456789abcdef01234567")
	t.Setenv("WARDEN_JWT_SIGNING_KEY", "jwt-signing-key--0123456789abcdef012345678")
	t.Setenv("WARDEN_CREDENTIAL_ENCRYPTION_KEY", "cred-encryption-key--0123456789abcdef0123456")
	t.Setenv("WARDEN_ADMIN_API_KEY", testAdminAPIKey)
	t.Setenv("WARDEN_ADMIN_PASSWORD", "test-admin-password-123")
	t.Setenv("WARDEN_ADMIN_USERNAME", "admin")

	appPool, err := postgres.ConnectPool(ctx)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	adminPool, err := postgres.ConnectAdminPool(ctx)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	h, err := NewHandler(appPool, adminPool)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	const tenant = "rls-http-acme"
	if _, err := ownerPool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1) ON CONFLICT (slug) DO NOTHING`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, `INSERT INTO tenant_quotas(tenant_id) VALUES ($1) ON CONFLICT DO NOTHING`, tenant); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = ownerPool.Exec(bg, `DELETE FROM admin_sessions WHERE principal_id IN (SELECT id FROM principals WHERE tenant_id=$1)`, tenant)
		_, _ = ownerPool.Exec(bg, `DELETE FROM principals WHERE tenant_id=$1`, tenant)
		_, _ = ownerPool.Exec(bg, `DELETE FROM tenant_quotas WHERE tenant_id=$1`, tenant)
		_, _ = ownerPool.Exec(bg, `DELETE FROM tenants WHERE slug=$1`, tenant)
	})
	hash, err := hashPassword("tenant-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash) VALUES ('user','acme-admin','ACTIVE',$1,'{"role":"tenant_admin"}',$2)`, tenant, hash); err != nil {
		t.Fatal(err)
	}

	// 租户管理员登录（app 角色，租户Scoped）。
	login := httptest.NewRecorder()
	h.Login(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewBufferString(`{"username":"acme-admin","password":"tenant-password-123","tenant":"`+tenant+`"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login under app role: status=%d body=%s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login issued no session cookie")
	}

	// auth/me（JWT 认证 + 租户Scoped principal 查询）。
	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookies[0])
	meReq.Header.Set("Origin", "http://localhost:4173")
	mux.ServeHTTP(me, meReq)
	if me.Code != http.StatusOK {
		t.Fatalf("auth/me under app role: status=%d body=%s", me.Code, me.Body.String())
	}
	var current struct {
		Tenant domain.Tenant `json:"tenant"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.Tenant.Slug != tenant {
		t.Fatalf("auth/me tenant=%s want %s", current.Tenant.Slug, tenant)
	}

	// 超管跨租户 ListAccounts（admin API key → 特权池）。
	resp := doAdminRequest(t, mux, http.MethodGet, "/api/v1/accounts", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("superadmin ListAccounts under app role: status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func rlsRoleURL(t *testing.T, ownerURL, user, password string) string {
	t.Helper()
	u, err := url.Parse(ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}