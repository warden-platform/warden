package postgres

import (
	"context"
	"os"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCrossTenantWriteGuards 回归 RLS 前置修复的应用层跨租户写漏洞：
// 即便 SQL 被传入他租户 ID，租户过滤/级联守卫也必须挡住写操作。
// （RLS 启用后这些是数据库层兜底之上的第一道防线。）
func TestCrossTenantWriteGuards(t *testing.T) {
	dsn := os.Getenv("WARDEN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WARDEN_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	const ta, tb = "ct-guard-a", "ct-guard-b"
	for _, tn := range []string{ta, tb} {
		if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1) ON CONFLICT (slug) DO NOTHING`, tn); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, tn := range []string{ta, tb} {
			_, _ = pool.Exec(bg, `DELETE FROM database_query_template_publish_requests WHERE tenant_id=$1`, tn)
			_, _ = pool.Exec(bg, `DELETE FROM database_query_templates WHERE tenant_id=$1`, tn)
			_, _ = pool.Exec(bg, `DELETE FROM integrations WHERE tenant_id=$1`, tn)
			_, _ = pool.Exec(bg, `DELETE FROM connectors WHERE tenant_id=$1`, tn)
			_, _ = pool.Exec(bg, `DELETE FROM principals WHERE tenant_id=$1`, tn)
			_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE slug=$1`, tn)
		}
	})

	ctxA := domain.WithTenantID(ctx, ta)
	ctxB := domain.WithTenantID(ctx, tb)

	// 在租户 B 种一个 principal 与一条 DRAFT 模板 + PENDING 发布请求。
	var victimID, connectorID, integrationID, templateID, requestID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,tenant_id) VALUES ('user','victim',$1) RETURNING id`, tb).Scan(&victimID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,tenant_id) VALUES ('c','jenkins',$1) RETURNING id`, tb).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO integrations(connector_id,protocol,name,endpoint,credential_ref,tenant_id) VALUES ($1,'jenkins','i','http://x','ref',$2) RETURNING id`, connectorID, tb).Scan(&integrationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO database_query_templates(integration_id,schema_resource_id,name,version,canonical_ast,template_hash,tenant_id) VALUES ($1,'s','t',1,'{}','h',$2) RETURNING id`, integrationID, tb).Scan(&templateID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO database_query_template_publish_requests(template_id,requested_by,tenant_id) VALUES ($1,'req',$2) RETURNING id`, templateID, tb).Scan(&requestID); err != nil {
		t.Fatal(err)
	}

	principals := NewPrincipalRepo(pool)
	templates := NewQueryTemplateRepo(pool)

	// 1) Delete 级联守卫：以租户 A 上下文删租户 B 的 principal，必须空操作。
	if err := principals.Delete(ctxA, victimID); err != nil {
		t.Fatalf("Delete wrong-tenant returned err: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM principals WHERE id=$1`, victimID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ACTIVE" {
		t.Fatalf("cross-tenant Delete disabled victim principal: status=%s", status)
	}

	// 2) ApprovePublish 租户隔离：租户 A 批准租户 B 的请求必须失败。
	if err := templates.ApprovePublish(ctxA, requestID, templateID, "attacker"); err == nil {
		t.Fatal("cross-tenant ApprovePublish succeeded; want error")
	}
	// 3) RejectPublish 同理。
	if err := templates.RejectPublish(ctxA, requestID, templateID, "x"); err == nil {
		t.Fatal("cross-tenant RejectPublish succeeded; want error")
	}
	var reqStatus, tplStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM database_query_template_publish_requests WHERE id=$1`, requestID).Scan(&reqStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM database_query_templates WHERE id=$1`, templateID).Scan(&tplStatus); err != nil {
		t.Fatal(err)
	}
	if reqStatus != "PENDING" || tplStatus != "DRAFT" {
		t.Fatalf("cross-tenant publish write leaked: request=%s template=%s", reqStatus, tplStatus)
	}

	// 4) 正向对照：租户 B 批准自己的请求必须成功（租户过滤不误伤合法流程）。
	if err := templates.ApprovePublish(ctxB, requestID, templateID, "owner"); err != nil {
		t.Fatalf("same-tenant ApprovePublish failed: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM database_query_template_publish_requests WHERE id=$1`, requestID).Scan(&reqStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM database_query_templates WHERE id=$1`, templateID).Scan(&tplStatus); err != nil {
		t.Fatal(err)
	}
	if reqStatus != "APPROVED" || tplStatus != "PUBLISHED" {
		t.Fatalf("same-tenant approve wrong outcome: request=%s template=%s", reqStatus, tplStatus)
	}
}