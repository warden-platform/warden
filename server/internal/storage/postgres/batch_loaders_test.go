package postgres

import (
	"context"
	"os"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This test is database-gated like the other storage integration tests. It
// verifies the batch loaders used by list enrichment return the right rows
// and never cross the tenant boundary.
func TestBatchLoadersRespectTenantBoundary(t *testing.T) {
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

	const tenant = "batch-loader-test"
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(slug,name) VALUES ($1,$1) ON CONFLICT (slug) DO NOTHING`, tenant); err != nil {
		t.Fatal(err)
	}
	var platformPrincipalIDs, platformConnectorIDs, platformResourceIDs []string
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM principal_api_keys WHERE principal_id IN (SELECT id FROM principals WHERE tenant_id=$1)`, tenant)
		_, _ = pool.Exec(bg, `DELETE FROM resources WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(bg, `DELETE FROM connectors WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(bg, `DELETE FROM principals WHERE tenant_id=$1`, tenant)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE slug=$1`, tenant)
		for _, id := range platformResourceIDs {
			_, _ = pool.Exec(bg, `DELETE FROM resources WHERE id=$1`, id)
		}
		for _, id := range platformConnectorIDs {
			_, _ = pool.Exec(bg, `DELETE FROM connectors WHERE id=$1`, id)
		}
		for _, id := range platformPrincipalIDs {
			_, _ = pool.Exec(bg, `DELETE FROM principal_api_keys WHERE principal_id=$1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM principals WHERE id=$1`, id)
		}
	})

	insertPrincipal := func(tenantID, name string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata) VALUES ('user',$1,'ACTIVE',$2,'{}') RETURNING id`, name, tenantID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	tenantPrincipalA := insertPrincipal(tenant, "batch-a")
	tenantPrincipalB := insertPrincipal(tenant, "batch-b")
	platformPrincipal := insertPrincipal("platform", "batch-platform")
	platformPrincipalIDs = append(platformPrincipalIDs, platformPrincipal)

	tenantCtx := domain.WithTenantID(ctx, tenant)
	principals := NewPrincipalRepo(pool)
	summaries, err := principals.SummariesByIDs(tenantCtx, []string{tenantPrincipalA, tenantPrincipalB, platformPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || summaries[tenantPrincipalA].Name != "batch-a" || summaries[tenantPrincipalB].Kind != domain.PrincipalKindUser {
		t.Fatalf("principal summaries leaked or missing rows: %#v", summaries)
	}
	if empty, err := principals.SummariesByIDs(tenantCtx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty ids: %#v %v", empty, err)
	}

	insertConnector := func(tenantID, name string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,version,capabilities,last_heartbeat,tenant_id)
			VALUES ($1,'kubernetes','ACTIVE','staging','1.0.0','[]'::jsonb,now(),$2) RETURNING id`, name, tenantID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	tenantConnector := insertConnector(tenant, "batch-conn")
	platformConnector := insertConnector("platform", "batch-conn-platform")
	platformConnectorIDs = append(platformConnectorIDs, platformConnector)
	insertResource := func(tenantID, connectorID, name string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,status,tenant_id)
			VALUES ('deployment',$1,$2,$3,'ACTIVE',$4) RETURNING id`, "ext-"+name, name, connectorID, tenantID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	tenantResourceA := insertResource(tenant, tenantConnector, "batch-res-a")
	tenantResourceB := insertResource(tenant, tenantConnector, "batch-res-b")
	platformResource := insertResource("platform", platformConnector, "batch-res-platform")
	platformResourceIDs = append(platformResourceIDs, platformResource)

	resources := NewResourceRepo(pool)
	byIDs, err := resources.ByIDs(tenantCtx, []string{tenantResourceA, tenantResourceB, platformResource})
	if err != nil {
		t.Fatal(err)
	}
	if len(byIDs) != 2 || byIDs[tenantResourceA].Name != "batch-res-a" || byIDs[tenantResourceB].ConnectorID != tenantConnector {
		t.Fatalf("resource ByIDs leaked or missing rows: %#v", byIDs)
	}
	values := make([]domain.Resource, 0, len(byIDs))
	for _, res := range byIDs {
		values = append(values, res)
	}
	if err := resources.EnrichProjectScopes(tenantCtx, values); err != nil {
		t.Fatal(err)
	}
	for _, res := range values {
		if res.ProjectIDs == nil {
			t.Fatalf("EnrichProjectScopes left nil ProjectIDs on %s", res.ID)
		}
	}

	insertKey := func(principalID, name string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO principal_api_keys(principal_id,name,key_prefix,key_hash)
			VALUES ($1,$2,'warden_agk_test',decode(repeat('ab',32),'hex')) RETURNING id`, principalID, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	tenantKey := insertKey(tenantPrincipalA, "batch-key")
	platformKey := insertKey(platformPrincipal, "batch-key-platform")

	keys := NewAPIKeyRepo(pool)
	names, err := keys.NamesByIDs(tenantCtx, []string{tenantKey, platformKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[tenantKey] != "batch-key" {
		t.Fatalf("key names leaked across tenants: %#v", names)
	}
}