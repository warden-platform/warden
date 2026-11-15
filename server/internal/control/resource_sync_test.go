package control

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"gitee.com/pam-platform/warden/server/internal/storage/postgres"
)

func TestResourceSyncSnapshotIsAtomic(t *testing.T) {
	pool := newTestDatabasePool(t)
	ctx := domain.WithTenantID(context.Background(), "platform")
	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors
		(name, connector_type, status, environment, version, capabilities, last_heartbeat, tenant_id)
		VALUES ('resource-sync-test', 'linux', 'ACTIVE', 'production', '1.0.0', '[]'::jsonb, now(), 'platform')
		RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	repo := postgres.NewResourceRepo(pool)
	resource := func(externalID, name string) domain.Resource {
		return domain.Resource{
			ResourceType: "systemd_service", ExternalID: externalID, Name: name,
			Namespace: "host-a", Environment: "production", Status: domain.ResourceActive,
			Labels: map[string]any{}, Annotations: map[string]any{}, Metadata: map[string]any{},
		}
	}

	stats, err := repo.SyncSnapshot(ctx, connectorID, "generation-1", []domain.Resource{
		resource("service-a", "service-a-old"), resource("service-b", "service-b"), resource("service-a", "service-a"),
	})
	if err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	if stats.Staged != 2 || stats.Upserted != 2 || stats.Orphaned != 0 {
		t.Fatalf("initial stats = %#v, want staged/upserted=2 orphaned=0", stats)
	}
	var resourceCount int
	var resourceAName string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(name) FILTER (WHERE external_id='service-a')
		FROM resources WHERE connector_id=$1`, connectorID).Scan(&resourceCount, &resourceAName); err != nil {
		t.Fatal(err)
	}
	if resourceCount != 2 || resourceAName != "service-a" {
		t.Fatalf("deduplicated snapshot count=%d service-a name=%q", resourceCount, resourceAName)
	}

	if _, err := pool.Exec(ctx, `UPDATE resources SET status='DISABLED' WHERE connector_id=$1 AND external_id='service-a'`, connectorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO resources
		(resource_type, external_id, name, namespace, connector_id, environment, status, tenant_id)
		VALUES ('systemd_service', 'legacy-service', 'legacy-service', 'host-a', $1, 'production', 'ACTIVE', 'platform')`, connectorID); err != nil {
		t.Fatal(err)
	}
	stats, err = repo.SyncSnapshot(ctx, connectorID, "generation-2", []domain.Resource{resource("service-a", "service-a-renamed")})
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if stats.Orphaned != 2 {
		t.Fatalf("second snapshot orphaned=%d, want 2", stats.Orphaned)
	}
	var statusA, statusB, legacyStatus domain.ResourceStatus
	if err := pool.QueryRow(ctx, `SELECT
		max(status::text) FILTER (WHERE external_id='service-a'),
		max(status::text) FILTER (WHERE external_id='service-b'),
		max(status::text) FILTER (WHERE external_id='legacy-service')
		FROM resources WHERE connector_id=$1`, connectorID).Scan(&statusA, &statusB, &legacyStatus); err != nil {
		t.Fatal(err)
	}
	if statusA != domain.ResourceDisabled || statusB != domain.ResourceOrphaned || legacyStatus != domain.ResourceOrphaned {
		t.Fatalf("statuses after missing snapshot: service-a=%s service-b=%s legacy=%s", statusA, statusB, legacyStatus)
	}

	if _, err := repo.SyncSnapshot(ctx, connectorID, "generation-3", []domain.Resource{
		resource("service-a", "service-a-renamed"), resource("service-b", "service-b"),
	}); err != nil {
		t.Fatalf("reactivate snapshot: %v", err)
	}
	_, err = repo.SyncSnapshot(ctx, connectorID, "generation-4", []domain.Resource{
		resource("service-a", strings.Repeat("x", 256)),
	})
	if err == nil {
		t.Fatal("oversized resource name unexpectedly succeeded")
	}
	var serviceBStatus domain.ResourceStatus
	var serviceBGeneration string
	if err := pool.QueryRow(ctx, `SELECT status, sync_generation FROM resources
		WHERE connector_id=$1 AND external_id='service-b'`, connectorID).Scan(&serviceBStatus, &serviceBGeneration); err != nil {
		t.Fatal(err)
	}
	if serviceBStatus != domain.ResourceActive || serviceBGeneration != "generation-3" {
		t.Fatalf("failed snapshot changed service-b: status=%s generation=%s", serviceBStatus, serviceBGeneration)
	}
}

func TestResourceSyncSnapshotRejectsOverlap(t *testing.T) {
	pool := newTestDatabasePool(t)
	ctx := domain.WithTenantID(context.Background(), "platform")
	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors
		(name, connector_type, status, environment, version, capabilities, last_heartbeat, tenant_id)
		VALUES ('resource-sync-lock-test', 'linux', 'ACTIVE', 'production', '1.0.0', '[]'::jsonb, now(), 'platform')
		RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(ctx)
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, connectorID); err != nil {
		t.Fatal(err)
	}

	_, err = postgres.NewResourceRepo(pool).SyncSnapshot(ctx, connectorID, "generation-1", nil)
	if !errors.Is(err, postgres.ErrResourceSyncInProgress) {
		t.Fatalf("overlapping snapshot error = %v, want ErrResourceSyncInProgress", err)
	}
}

func TestResourceStatsCatalogCountsByProjectDomainAndStatus(t *testing.T) {
	pool := newTestDatabasePool(t)
	ctx := domain.WithTenantID(context.Background(), "platform")
	var defaultProjectID, applicationProjectID, connectorID, integrationID string
	if err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE is_default`).Scan(&defaultProjectID); err != nil {
		t.Fatalf("get default project: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects (name, description, tenant_id) VALUES ('catalog-app', 'catalog stats test', 'platform') RETURNING id`).Scan(&applicationProjectID); err != nil {
		t.Fatalf("insert application project: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors
		(name, connector_type, status, environment, version, capabilities, last_heartbeat, project_id, tenant_id)
		VALUES ('catalog-stats-test', 'kubernetes', 'ACTIVE', 'production', '1.0.0', '[]'::jsonb, now(), $1, 'platform')
		RETURNING id`, applicationProjectID).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO integrations
		(connector_id, protocol, name, endpoint, credential_ref, status, project_id, tenant_id)
		VALUES ($1, 'jenkins', 'catalog-jenkins', 'https://jenkins.example.test', 'pam:test', 'ACTIVE', $2, 'platform')
		RETURNING id`, connectorID, defaultProjectID).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO resources
		(resource_type, external_id, name, namespace, connector_id, integration_id, environment, status, tenant_id)
		VALUES
		('deployment', 'apps/api', 'api', 'apps', $1, NULL, 'production', 'ACTIVE', 'platform'),
		('systemd_service', 'host/api.service', 'api.service', 'host', $1, NULL, 'production', 'DISABLED', 'platform'),
		('network_device', 'h3c/device/core-01', 'core-01', '', $1, NULL, 'production', 'ACTIVE', 'platform'),
		('jenkins_controller', 'controller', 'controller', '', $1, $2, 'production', 'ACTIVE', 'platform'),
		('jenkins_job', 'job/team/release', 'team/release', '', $1, $2, 'production', 'ORPHANED', 'platform'),
		('jenkins_build', 'build/team/release/10', '#10', 'team/release', $1, $2, 'production', 'ACTIVE', 'platform'),
		('jenkins_queue_item', 'queue/10', '#10', '', $1, $2, 'production', 'ACTIVE', 'platform'),
		('jenkins_credential_reference', 'credential/deploy', 'deploy', '', $1, $2, 'production', 'ACTIVE', 'platform')`, connectorID, integrationID); err != nil {
		t.Fatalf("insert resources: %v", err)
	}
	var deploymentID string
	if err := pool.QueryRow(ctx, `SELECT id FROM resources WHERE external_id='apps/api' AND tenant_id='platform'`).Scan(&deploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE resources SET project_scope_mode='SELECTED_PROJECTS' WHERE id=$1`, deploymentID); err != nil {
		t.Fatalf("set deployment scope mode: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO resource_project_bindings(tenant_id,resource_id,project_id)
		VALUES ('platform',$1,$2),('platform',$1,$3)`, deploymentID, applicationProjectID, defaultProjectID); err != nil {
		t.Fatalf("share deployment across projects: %v", err)
	}

	stats, err := postgres.NewResourceRepo(pool).Stats(ctx)
	if err != nil {
		t.Fatalf("resource stats: %v", err)
	}
	catalog, ok := stats["catalogByProjectDomainStatus"].(map[string]map[string]map[string]int)
	if !ok {
		t.Fatalf("catalog stats type = %T", stats["catalogByProjectDomainStatus"])
	}
	if got := catalog[applicationProjectID]["kubernetes"]["ACTIVE"]; got != 1 {
		t.Fatalf("application kubernetes active = %d, want 1", got)
	}
	if got := catalog[defaultProjectID]["kubernetes"]["ACTIVE"]; got != 1 {
		t.Fatalf("default kubernetes active = %d, want shared deployment", got)
	}
	if got := catalog[applicationProjectID]["linux"]["DISABLED"]; got != 1 {
		t.Fatalf("application linux disabled = %d, want 1", got)
	}
	if got := catalog[applicationProjectID]["network"]["ACTIVE"]; got != 1 {
		t.Fatalf("application network active = %d, want 1", got)
	}
	if got := catalog[defaultProjectID]["jenkins"]["ACTIVE"]; got != 1 {
		t.Fatalf("default Jenkins active = %d, want only the visible controller", got)
	}
	if got := catalog[defaultProjectID]["jenkins"]["ORPHANED"]; got != 1 {
		t.Fatalf("default Jenkins orphaned = %d, want the visible job", got)
	}
	overall, ok := stats["catalogByDomainStatus"].(map[string]map[string]int)
	if !ok {
		t.Fatalf("overall catalog stats type = %T", stats["catalogByDomainStatus"])
	}
	if got := overall["kubernetes"]["ACTIVE"]; got != 1 {
		t.Fatalf("all-project Kubernetes active = %d, want unique shared deployment", got)
	}

	resources, err := postgres.NewResourceRepo(pool).List(ctx, "", "network", "", "", "", 0)
	if err != nil {
		t.Fatalf("list network resources: %v", err)
	}
	if len(resources) != 1 || resources[0].ResourceType != "network_device" || resources[0].Name != "core-01" {
		t.Fatalf("network resources = %#v, want only core-01 network_device", resources)
	}
}
