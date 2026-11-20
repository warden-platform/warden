package postgres

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRLSFirstBatchEnforcesIsolation 以真实 warden_app 角色（受 FORCE RLS
// 约束）验证首批表（executions 等）的数据库层隔离：本租户可见、跨租户读/写
// 被拒、空租户 fail-closed；合法旁路（特权池 ListAllTenants）可见全部。
//
// 应用池经 connectPoolFromDSN 建立（带 BeforeAcquire 租户 GUC 注入），与生产
// 路径一致；种子数据以 superuser owner 池写入（迁移角色绕过 RLS）。
func TestRLSFirstBatchEnforcesIsolation(t *testing.T) {
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

	// 建角色 + 注入测试密码（幂等）。
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
		t.Fatalf("app pool: %v", err)
	}
	defer appPool.Close()
	adminPool, err := connectPoolFromDSN(ctx, roleURL(t, ownerURL, "warden_privileged", "testpriv"))
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()

	const ta, tb = "rls-batch-a", "rls-batch-b"
	for _, tn := range []string{ta, tb} {
		if _, err := ownerPool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1) ON CONFLICT (slug) DO NOTHING`, tn); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, tn := range []string{ta, tb} {
			_, _ = ownerPool.Exec(bg, `DELETE FROM executions WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM resources WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM connectors WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM principals WHERE tenant_id=$1`, tn)
			_, _ = ownerPool.Exec(bg, `DELETE FROM tenants WHERE slug=$1`, tn)
		}
	})

	var actionID string
	if err := ownerPool.QueryRow(ctx, `SELECT id FROM action_definitions LIMIT 1`).Scan(&actionID); err != nil {
		t.Fatalf("no action_definitions seed: %v", err)
	}
	seedExec := func(tn string) string {
		var connectorID, resourceID, principalID, execID string
		if err := ownerPool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,tenant_id) VALUES ('c','kubernetes',$1) RETURNING id`, tn).Scan(&connectorID); err != nil {
			t.Fatal(err)
		}
		if err := ownerPool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,tenant_id) VALUES ('deployment','ext','r',$1,$2) RETURNING id`, connectorID, tn).Scan(&resourceID); err != nil {
			t.Fatal(err)
		}
		if err := ownerPool.QueryRow(ctx, `INSERT INTO principals(kind,name,tenant_id) VALUES ('user','p',$1) RETURNING id`, tn).Scan(&principalID); err != nil {
			t.Fatal(err)
		}
		if err := ownerPool.QueryRow(ctx, `INSERT INTO executions(principal_id,resource_id,action_id,parameters,parameter_hash,trace_id,connector_id,tenant_id) VALUES ($1,$2,$3,'{}','h','tr',$4,$5) RETURNING id`, principalID, resourceID, actionID, connectorID, tn).Scan(&execID); err != nil {
			t.Fatal(err)
		}
		return execID
	}
	execA := seedExec(ta)
	execB := seedExec(tb)

	repo := NewExecutionRepo(appPool)
	repo.SetAdminPool(adminPool)
	ctxA := domain.WithTenantID(ctx, ta)
	ctxB := domain.WithTenantID(ctx, tb)

	// 本租户可见。
	if e, err := repo.GetByID(ctxA, execA); err != nil || e == nil {
		t.Fatalf("own-tenant read failed: e=%v err=%v", e, err)
	}
	// 跨租户读被 RLS 拒绝。
	if e, err := repo.GetByID(ctxA, execB); err != nil || e != nil {
		t.Fatalf("cross-tenant read leaked: e=%v err=%v", e, err)
	}
	if e, err := repo.GetByID(ctxB, execB); err != nil || e == nil {
		t.Fatalf("tenant B own read failed: e=%v err=%v", e, err)
	}
	// 空租户 fail-closed。
	if e, err := repo.GetByID(ctx, execA); err != nil || e != nil {
		t.Fatalf("empty-tenant read should be blocked: e=%v err=%v", e, err)
	}
	// 跨租户写被 RLS 拒绝（TransitionStatus 影响 0 行）。
	changed, err := repo.TransitionStatus(ctxA, execB, domain.ExecQueued, domain.ExecCancelled, "", "x")
	if err != nil {
		t.Fatalf("cross-tenant write err: %v", err)
	}
	if changed {
		t.Fatal("cross-tenant write succeeded; RLS should block")
	}
	var st string
	if err := ownerPool.QueryRow(ctx, `SELECT status FROM executions WHERE id=$1`, execB).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != "QUEUED" {
		t.Fatalf("cross-tenant write mutated victim: status=%s", st)
	}

	// 合法旁路：特权池 ListAllTenants 可见两个租户。
	all, err := repo.ListAllTenants(ctxA, "", 100, 0)
	if err != nil {
		t.Fatalf("admin ListAllTenants: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range all {
		seen[e.ID] = true
	}
	if !seen[execA] || !seen[execB] {
		t.Fatalf("admin bypass should see both tenants, saw %v", seen)
	}
}

func roleURL(t *testing.T, ownerURL, user, password string) string {
	t.Helper()
	u, err := url.Parse(ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}