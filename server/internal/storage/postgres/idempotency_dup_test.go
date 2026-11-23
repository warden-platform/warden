package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCreateExecutionDuplicateIdempotency 确定性验证部分唯一索引 + ON CONFLICT
// DO NOTHING：相同 (principal_id, idempotency_key) 第二次插入返回
// ErrDuplicateIdempotency，供 handler 回查幂等。
func TestCreateExecutionDuplicateIdempotency(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,tenant_id) VALUES ('idem-dup-c','kubernetes','platform') RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,tenant_id) VALUES ('deployment','idem-ext','r',$1,'platform') RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM action_definitions WHERE tenant_id='platform' LIMIT 1`).Scan(&actionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,tenant_id) VALUES ('user','idem-p','platform') RETURNING id`).Scan(&principalID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM executions WHERE resource_id=$1`, resourceID)
		_, _ = pool.Exec(bg, `DELETE FROM resources WHERE id=$1`, resourceID)
		_, _ = pool.Exec(bg, `DELETE FROM connectors WHERE id=$1`, connectorID)
		_, _ = pool.Exec(bg, `DELETE FROM principals WHERE id=$1`, principalID)
	})

	newExec := func() *domain.Execution {
		return &domain.Execution{
			PrincipalID:     principalID,
			ResourceID:      resourceID,
			ActionID:        actionID,
			Parameters:      map[string]any{},
			ParameterHash:   "h",
			TraceID:         "tr",
			ConnectorID:     connectorID,
			Status:          domain.ExecQueued,
			IdempotencyKey:  "idem-dup-key",
			ExecutorType:    "connector",
			TimeoutSeconds:  300,
			Reason:          "t",
			ExternalContext: map[string]any{},
		}
	}

	if err := createExecution(ctx, pool, newExec()); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err = createExecution(ctx, pool, newExec())
	if !errors.Is(err, ErrDuplicateIdempotency) {
		t.Fatalf("second insert err = %v, want ErrDuplicateIdempotency", err)
	}
}