package postgres

import (
	"context"
	"os"
	"testing"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This test is intentionally database-gated. It exercises the same concrete
// repositories used by the HTTP handlers, rather than testing SQL fragments
// against mocks.
func TestTenantRepositoriesIsolateDataAndShareActions(t *testing.T) {
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
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	const tenant = "isolation-test-acme"
	_, _ = pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1) ON CONFLICT (slug) DO NOTHING`, tenant)
	defer pool.Exec(ctx, `DELETE FROM tenants WHERE slug=$1`, tenant)
	defer pool.Exec(ctx, `DELETE FROM principals WHERE tenant_id=$1`, tenant)

	var acmeID, platformID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata) VALUES ('user','same-isolation-name','ACTIVE',$1,'{}') RETURNING id`, tenant).Scan(&acmeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata) VALUES ('user','same-isolation-name','ACTIVE','platform','{}') RETURNING id`).Scan(&platformID); err != nil {
		t.Fatal(err)
	}

	repo := NewPrincipalRepo(pool)
	acmeCtx := domain.WithTenantID(ctx, tenant)
	platformCtx := domain.WithTenantID(ctx, "platform")
	got, err := repo.GetByID(acmeCtx, acmeID)
	if err != nil || got == nil || got.TenantID != tenant {
		t.Fatalf("acme lookup = %#v, err=%v", got, err)
	}
	if got, err := repo.GetByID(acmeCtx, platformID); err != nil || got != nil {
		t.Fatalf("cross-tenant lookup returned %#v, err=%v", got, err)
	}
	if got, err := repo.GetByName(platformCtx, "same-isolation-name"); err != nil || got == nil || got.ID != platformID {
		t.Fatalf("platform lookup = %#v, err=%v", got, err)
	}

	actions := NewActionRepo(pool)
	shared, err := actions.GetByName(acmeCtx, "database.query.preview")
	if err != nil || shared == nil || shared.TenantID != "platform" {
		t.Fatalf("shared action lookup = %#v, err=%v", shared, err)
	}
}
