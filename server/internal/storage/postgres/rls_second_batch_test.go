package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRLSSecondBatchIsolationAndBypass 以真实 warden_app 角色验证第二批表
// 隔离（strict + action_definitions 共享读）与旁路方法路由（认证/钉钉/联邦/
// 过期扫描走特权池）。旁路方法若未路由会在 app 角色下 fail-closed 失败。
func TestRLSSecondBatchIsolationAndBypass(t *testing.T) {
	ownerURL := os.Getenv("WARDEN_TEST_DATABASE_URL")
	if ownerURL == "" {
		t.Skip("WARDEN_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	ownerPool, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerPool.Close()

	rolesSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "db", "roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, string(rolesSQL)); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerPool.Exec(ctx, `ALTER ROLE warden_app LOGIN PASSWORD 'testapp'; ALTER ROLE warden_privileged LOGIN PASSWORD 'testpriv';`); err != nil {
		t.Fatal(err)
	}
	appPool, err := connectPoolFromDSN(ctx, roleURL(t, ownerURL, "warden_app", "testapp"))
	if err != nil {
		t.Fatal(err)
	}
	defer appPool.Close()
	adminPool, err := connectPoolFromDSN(ctx, roleURL(t, ownerURL, "warden_privileged", "testpriv"))
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()

	const ta, tb = "rls2-a", "rls2-b"
	for _, tn := range []string{ta, tb} {
		if _, err := ownerPool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1) ON CONFLICT (slug) DO NOTHING`, tn); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, tn := range []string{ta, tb} {
			_, _ = ownerPool.Exec(bg, `DELETE FROM principal_api_keys WHERE principal_id IN (SELECT id FROM principals WHERE tenant_id=$1)`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM notification_channels WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM resources WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM connectors WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM principals WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM tenants WHERE slug=$1`, tn)
		}
	})

	// 种双租户 principal + connector + resource。
	seed := func(tn string) (principalID, resourceID string) {
		var cid string
		if err := ownerPool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,tenant_id) VALUES ('c','kubernetes',$1) RETURNING id`, tn).Scan(&cid); err != nil {
			t.Fatal(err)
		}
		if err := ownerPool.QueryRow(ctx, `INSERT INTO principals(kind,name,tenant_id) VALUES ('user','p',$1) RETURNING id`, tn).Scan(&principalID); err != nil {
			t.Fatal(err)
		}
		if err := ownerPool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,tenant_id) VALUES ('deployment','ext','r',$1,$2) RETURNING id`, cid, tn).Scan(&resourceID); err != nil {
			t.Fatal(err)
		}
		return
	}
	pA, rA := seed(ta)
	pB, rB := seed(tb)

	ctxA := domain.WithTenantID(ctx, ta)
	ctxB := domain.WithTenantID(ctx, tb)

	resources := NewResourceRepo(appPool)
	principals := NewPrincipalRepo(appPool)
	principals.SetAdminPool(adminPool)

	// strict 隔离：resources / principals 本租户可见、跨租户拒。
	if r, err := resources.GetByID(ctxA, rA); err != nil || r == nil {
		t.Fatalf("resources own read: r=%v err=%v", r, err)
	}
	if r, err := resources.GetByID(ctxA, rB); err != nil || r != nil {
		t.Fatalf("resources cross-tenant leaked: r=%v err=%v", r, err)
	}
	if p, err := principals.GetByID(ctxA, pA); err != nil || p == nil {
		t.Fatalf("principals own read: p=%v err=%v", p, err)
	}
	if p, err := principals.GetByID(ctxA, pB); err != nil || p != nil {
		t.Fatalf("principals cross-tenant leaked: p=%v err=%v", p, err)
	}

	// action_definitions 共享读：platform 动作对两个租户都可见。
	var platformAction string
	if err := ownerPool.QueryRow(ctx, `SELECT id FROM action_definitions WHERE tenant_id='platform' LIMIT 1`).Scan(&platformAction); err != nil {
		t.Fatalf("no platform action: %v", err)
	}
	actions := NewActionRepo(appPool)
	if m, err := actions.SummariesByIDs(ctxA, []string{platformAction}); err != nil || m[platformAction].ID == "" {
		t.Fatalf("shared action not visible to tenant A: m=%v err=%v", m, err)
	}
	if m, err := actions.SummariesByIDs(ctxB, []string{platformAction}); err != nil || m[platformAction].ID == "" {
		t.Fatalf("shared action not visible to tenant B: m=%v err=%v", m, err)
	}

	// 旁路：APIKeyRepo.GetForAuthentication（无租户 ctx，特权池）须能认证。
	var keyID string
	if err := ownerPool.QueryRow(ctx, `INSERT INTO principal_api_keys(id,principal_id,name,key_prefix,key_hash,status,purpose,audience,scopes) VALUES (gen_random_uuid(),$1,'k','pref','0123456789abcdef0123456789abcdef','ACTIVE','REGISTRATION','aud','[]') RETURNING id`, pB).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	apiKeys := NewAPIKeyRepo(appPool)
	apiKeys.SetAdminPool(adminPool)
	if k, p, err := apiKeys.GetForAuthentication(ctx, keyID); err != nil || k == nil || p == nil {
		t.Fatalf("GetForAuthentication bypass failed: k=%v p=%v err=%v", k, p, err)
	}

	// 旁路：NotificationChannelRepo.GetByConversation（登录前跨租户，特权池）。
	if _, err := ownerPool.Exec(ctx, `INSERT INTO notification_channels(type,name,enabled,events,tenant_id,open_conversation_id) VALUES ('dingtalk','g',true,'{}',$1,'cid-rls2')`, tb); err != nil {
		t.Fatal(err)
	}
	channels := NewNotificationChannelRepo(appPool)
	channels.SetAdminPool(adminPool)
	if ch, err := channels.GetByConversation(ctx, "dingtalk", "cid-rls2"); err != nil || ch == nil || ch.TenantID != tb {
		t.Fatalf("GetByConversation bypass failed: ch=%v err=%v", ch, err)
	}

	// 旁路：RequestRepo.ListExpiredPending（worker 全表扫描，特权池）。
	requests := NewRequestRepo(appPool)
	requests.SetAdminPool(adminPool)
	if _, err := requests.ListExpiredPending(ctx, 10); err != nil {
		t.Fatalf("ListExpiredPending bypass failed: %v", err)
	}
}