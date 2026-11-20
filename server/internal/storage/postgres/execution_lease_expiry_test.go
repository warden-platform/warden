package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestExecutionLeaseExpiry 覆盖此前零测试的租约回收 fail-safe：
// ExpireDispatchLeases（租约过期 / 无租约超 5 分钟 → UNKNOWN，禁自动重试）
// 与 ExpireQueuedExecutions（grant 过期 → EXPIRED）。
func TestExecutionLeaseExpiry(t *testing.T) {
	dsn := os.Getenv("WARDEN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WARDEN_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var connectorID, resourceID, actionID, principalID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,tenant_id) VALUES ('lease-c','kubernetes','platform') RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,tenant_id) VALUES ('deployment','lease-ext','r',$1,'platform') RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM action_definitions WHERE tenant_id='platform' LIMIT 1`).Scan(&actionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,tenant_id) VALUES ('user','lease-p','platform') RETURNING id`).Scan(&principalID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM executions WHERE resource_id=$1`, resourceID)
		_, _ = pool.Exec(bg, `DELETE FROM access_grants WHERE principal_id=$1`, principalID)
		_, _ = pool.Exec(bg, `DELETE FROM resources WHERE id=$1`, resourceID)
		_, _ = pool.Exec(bg, `DELETE FROM connectors WHERE id=$1`, connectorID)
		_, _ = pool.Exec(bg, `DELETE FROM principals WHERE id=$1`, principalID)
	})

	repo := NewExecutionRepo(pool)
	seedDispatched := func(expiresExpr string, updatedExpr string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO executions(principal_id,resource_id,action_id,parameters,parameter_hash,trace_id,connector_id,tenant_id,status,dispatch_token,dispatch_expires_at,updated_at) VALUES ($1,$2,$3,'{}','h','tr',$4,'platform','DISPATCHED',gen_random_uuid(),`+expiresExpr+`,`+updatedExpr+`) RETURNING id`, principalID, resourceID, actionID, connectorID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	expired := seedDispatched("now()-interval '1 hour'", "now()")
	fresh := seedDispatched("now()+interval '1 hour'", "now()")
	noLeaseStale := seedDispatched("NULL", "now()-interval '10 minutes'")

	if _, err := repo.ExpireDispatchLeases(ctx); err != nil {
		t.Fatalf("ExpireDispatchLeases: %v", err)
	}
	assertStatus := func(id, want, wantCode string) {
		var st, code string
		if err := pool.QueryRow(ctx, `SELECT status, COALESCE(error_code,'') FROM executions WHERE id=$1`, id).Scan(&st, &code); err != nil {
			t.Fatal(err)
		}
		if st != want || (wantCode != "" && code != wantCode) {
			t.Fatalf("exec %s: status=%s code=%s, want %s/%s", id, st, code, want, wantCode)
		}
	}
	assertStatus(expired, "UNKNOWN", "DISPATCH_LEASE_EXPIRED")
	assertStatus(noLeaseStale, "UNKNOWN", "DISPATCH_LEASE_EXPIRED")
	assertStatus(fresh, "DISPATCHED", "")

	// ExpireQueuedExecutions：grant 过期 → 执行 EXPIRED、grant EXPIRED。
	var grantID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_grants(principal_id,resource_scope,action_scope,parameter_hash,scope_hash,max_uses,expires_at,status,tenant_id) VALUES ($1,'{}','{}','h','sh',1,now()-interval '1 hour','ACTIVE','platform') RETURNING id`, principalID).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	var queuedID string
	if err := pool.QueryRow(ctx, `INSERT INTO executions(principal_id,resource_id,action_id,parameters,parameter_hash,trace_id,connector_id,tenant_id,status,grant_id,expires_at) VALUES ($1,$2,$3,'{}','h','tr',$4,'platform','QUEUED',$5,now()+interval '1 hour') RETURNING id`, principalID, resourceID, actionID, connectorID, grantID).Scan(&queuedID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExpireQueuedExecutions(ctx); err != nil {
		t.Fatalf("ExpireQueuedExecutions: %v", err)
	}
	assertStatus(queuedID, "EXPIRED", "EXECUTION_GRANT_EXPIRED")
	var grantStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM access_grants WHERE id=$1`, grantID).Scan(&grantStatus); err != nil {
		t.Fatal(err)
	}
	if grantStatus != "EXPIRED" {
		t.Fatalf("expired grant status=%s want EXPIRED", grantStatus)
	}
}