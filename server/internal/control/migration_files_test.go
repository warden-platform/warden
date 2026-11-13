package control

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMigrationNumbersAreUnique(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	numberPattern := regexp.MustCompile(`^(\d+)_.*\.sql$`)
	seen := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := numberPattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		if previous, exists := seen[matches[1]]; exists {
			t.Errorf("migration number %s is used by both %s and %s", matches[1], previous, entry.Name())
		}
		seen[matches[1]] = entry.Name()
	}
}

func TestReleaseMigrationManifestIncludesAllReleaseMigrations(t *testing.T) {
	migrationsDir := filepath.Join("..", "..", "migrations")
	manifestPath := filepath.Join("..", "..", "..", "deploy", "release", "migrations.txt")

	manifest, err := os.Open(manifestPath)
	if err != nil {
		t.Fatalf("open release migration manifest: %v", err)
	}
	defer manifest.Close()

	listed := make(map[string]bool)
	scanner := bufio.NewScanner(manifest)
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name != "" {
			listed[name] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read release migration manifest: %v", err)
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		matches := regexp.MustCompile(`^(\d+)_`).FindStringSubmatch(entry.Name())
		if matches != nil && matches[1] >= "035" && !listed[entry.Name()] {
			t.Errorf("release migration manifest is missing %s", entry.Name())
		}
	}
}

func TestMultiTenantMigrationDeduplicatesProviderCapabilitiesBeforeTenantRewrite(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "migrations", "088_multitenancy_foundation.sql"))
	if err != nil {
		t.Fatalf("read multi-tenant migration: %v", err)
	}

	sql := string(data)
	deduplicateAt := strings.Index(sql, "DELETE FROM provider_capabilities legacy")
	rewriteAt := strings.Index(sql, "UPDATE provider_capabilities SET tenant_id='platform'")
	if deduplicateAt < 0 {
		t.Fatal("multi-tenant migration must remove conflicting legacy provider capabilities")
	}
	if rewriteAt < 0 || deduplicateAt > rewriteAt {
		t.Fatal("provider capability conflicts must be removed before tenant ownership is rewritten")
	}
}

func TestMultiTenantMigrationDeduplicatesSharedResourcesBeforeTenantRewrite(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "migrations", "088_multitenancy_foundation.sql"))
	if err != nil {
		t.Fatalf("read multi-tenant migration: %v", err)
	}

	sql := string(data)
	deduplicateAt := strings.Index(sql, "DELETE FROM resources legacy")
	rewriteAt := strings.Index(sql, "UPDATE resources SET tenant_id='platform'")
	if deduplicateAt < 0 {
		t.Fatal("multi-tenant migration must remove conflicting legacy shared resources")
	}
	if rewriteAt < 0 || deduplicateAt > rewriteAt {
		t.Fatal("shared resource conflicts must be removed before tenant ownership is rewritten")
	}
}

func TestControlPlaneMigrationSeedsFormalTenant(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "migrations", "067_control_plane_executor.sql"))
	if err != nil {
		t.Fatalf("read control-plane migration: %v", err)
	}

	sql := string(data)
	if strings.Contains(sql, "'control_plane', 'testing', 'ACTIVE', '{\"managedBy\":\"control_plane\"}'::jsonb, 'default'") {
		t.Fatal("control-plane migration must not recreate the legacy default-tenant resource")
	}
	if !strings.Contains(sql, "'control_plane', 'testing', 'ACTIVE', '{\"managedBy\":\"control_plane\"}'::jsonb, 'platform'") {
		t.Fatal("control-plane migration must seed the shared resource under platform")
	}
}

func TestReleaseMigrationsCanBeReapplied(t *testing.T) {
	pool := newTestDatabasePool(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire test connection: %v", err)
	}
	defer conn.Release()

	manifest, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "release", "migrations.txt"))
	if err != nil {
		t.Fatalf("read release migration manifest: %v", err)
	}
	for _, name := range strings.Split(string(manifest), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		content, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatalf("read release migration %s: %v", name, err)
		}
		if _, err := conn.Exec(ctx, string(content)); err != nil {
			t.Fatalf("reapply release migration %s: %v", name, err)
		}
	}

	var legacySharedResources int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM resources
		WHERE tenant_id='default' AND management_type='control_plane'`).Scan(&legacySharedResources); err != nil {
		t.Fatalf("count legacy shared resources: %v", err)
	}
	if legacySharedResources != 0 {
		t.Fatalf("legacy shared resources after migration replay = %d, want 0", legacySharedResources)
	}
}
