package postgres

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAppRoleGrantsComplete 验证 RLS 地基（P0）：warden_app/warden_privileged
// 角色属性正确，且 warden_app 对 public 下所有表拥有完整 DML 授权——RLS 启用
// 前先把「漏授导致运行时 permission denied」这类问题暴露出来。
//
// 以 owner（WARDEN_TEST_DATABASE_URL，superuser）执行 deploy/db/roles.sql 建角色
// +授权并注入测试密码，再断言角色属性与逐表授权，最后以 warden_app 真实连接
// 跑 SELECT 与一个回滚事务内的 INSERT/UPDATE/DELETE，证明授权可实际执行。
func TestAppRoleGrantsComplete(t *testing.T) {
	ownerURL := os.Getenv("WARDEN_TEST_DATABASE_URL")
	if ownerURL == "" {
		t.Skip("WARDEN_TEST_DATABASE_URL not set; skipping DB-gated role grant test")
	}
	ctx := context.Background()

	ownerPool, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatalf("connect owner: %v", err)
	}
	defer ownerPool.Close()

	rolesSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "db", "roles.sql"))
	if err != nil {
		t.Fatalf("read roles.sql: %v", err)
	}
	if _, err := ownerPool.Exec(ctx, string(rolesSQL)); err != nil {
		t.Fatalf("apply roles.sql: %v", err)
	}
	if _, err := ownerPool.Exec(ctx,
		`ALTER ROLE warden_app LOGIN PASSWORD 'testapp'; ALTER ROLE warden_privileged LOGIN PASSWORD 'testpriv';`); err != nil {
		t.Fatalf("set role passwords: %v", err)
	}

	// 角色属性：app 必须非 superuser/非 BYPASSRLS（否则 RLS 无效）；privileged 必须 BYPASSRLS。
	var appSuper, appBypass, privBypass bool
	if err := ownerPool.QueryRow(ctx,
		`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname='warden_app'`).Scan(&appSuper, &appBypass); err != nil {
		t.Fatalf("query warden_app attrs: %v", err)
	}
	if appSuper || appBypass {
		t.Fatalf("warden_app must be NOSUPERUSER NOBYPASSRLS, got super=%v bypass=%v", appSuper, appBypass)
	}
	if err := ownerPool.QueryRow(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname='warden_privileged'`).Scan(&privBypass); err != nil {
		t.Fatalf("query warden_privileged attrs: %v", err)
	}
	if !privBypass {
		t.Fatalf("warden_privileged must be BYPASSRLS")
	}

	// 逐表授权完整性：public 下每张表都必须有 SELECT/INSERT/UPDATE/DELETE。
	rows, err := ownerPool.Query(ctx, `
		SELECT c.relname,
		       has_table_privilege('warden_app', c.oid, 'SELECT'),
		       has_table_privilege('warden_app', c.oid, 'INSERT'),
		       has_table_privilege('warden_app', c.oid, 'UPDATE'),
		       has_table_privilege('warden_app', c.oid, 'DELETE')
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND c.relkind='r'
		ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("query table privileges: %v", err)
	}
	defer rows.Close()
	tableCount := 0
	for rows.Next() {
		var name string
		var sel, ins, upd, del bool
		if err := rows.Scan(&name, &sel, &ins, &upd, &del); err != nil {
			t.Fatalf("scan privilege: %v", err)
		}
		tableCount++
		if name == "audit_events" {
			// append-only：可读可写新事件，但应用角色不得改写/删除历史。
			if !sel || !ins || upd || del {
				t.Errorf("audit_events must be append-only for warden_app (select=%v insert=%v update=%v delete=%v)", sel, ins, upd, del)
			}
			continue
		}
		if !sel || !ins || !upd || !del {
			t.Errorf("warden_app missing DML grant on %s (select=%v insert=%v update=%v delete=%v)", name, sel, ins, upd, del)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate privileges: %v", err)
	}
	if tableCount == 0 {
		t.Fatal("no public tables found; schema not prepared")
	}

	var privSel, privIns, privUpd, privDel bool
	if err := ownerPool.QueryRow(ctx, `
		SELECT has_table_privilege('warden_privileged','public.audit_events','SELECT'),
		       has_table_privilege('warden_privileged','public.audit_events','INSERT'),
		       has_table_privilege('warden_privileged','public.audit_events','UPDATE'),
		       has_table_privilege('warden_privileged','public.audit_events','DELETE')`).Scan(&privSel, &privIns, &privUpd, &privDel); err != nil {
		t.Fatalf("query privileged audit_events privileges: %v", err)
	}
	if !privSel || !privIns || privUpd || privDel {
		t.Errorf("audit_events must be append-only for warden_privileged (select=%v insert=%v update=%v delete=%v)", privSel, privIns, privUpd, privDel)
	}

	// 以 warden_app 真实连接，证明 LOGIN + schema USAGE + DML 可执行。
	u, err := url.Parse(ownerURL)
	if err != nil {
		t.Fatalf("parse owner url: %v", err)
	}
	u.User = url.UserPassword("warden_app", "testapp")
	appPool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("connect as warden_app: %v", err)
	}
	defer appPool.Close()

	var n int
	if err := appPool.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&n); err != nil {
		t.Fatalf("warden_app SELECT tenants: %v", err)
	}

	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("warden_app begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO tenants (slug, name) VALUES ('rls-grant-probe', 'probe')`); err != nil {
		t.Fatalf("warden_app INSERT tenants: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tenants SET name='probe2' WHERE slug='rls-grant-probe'`); err != nil {
		t.Fatalf("warden_app UPDATE tenants: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM tenants WHERE slug='rls-grant-probe'`); err != nil {
		t.Fatalf("warden_app DELETE tenants: %v", err)
	}
	// 显式回滚（defer 已保证），不留探针数据。
	_ = tx.Rollback(ctx)

	// 特权角色可连接（旁路池地基）。
	u.User = url.UserPassword("warden_privileged", "testpriv")
	privPool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("connect as warden_privileged: %v", err)
	}
	defer privPool.Close()
	if err := privPool.QueryRow(ctx, `SELECT 1`).Scan(&n); err != nil {
		t.Fatalf("warden_privileged SELECT: %v", err)
	}
}