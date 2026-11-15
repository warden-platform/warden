package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestProvisionIdentityIntegration covers the branch that unit tests cannot
// reach without a real database: first-time federated provisioning and the
// role-preservation rule on subsequent logins. It is opt-in because it writes
// to a live PostgreSQL instance; set WARDEN_TEST_DATABASE_URL to enable it.
func TestProvisionIdentityIntegration(t *testing.T) {
	dsn := os.Getenv("WARDEN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set WARDEN_TEST_DATABASE_URL to run the federated provisioning integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Cleanups run LIFO: the data cleanup below is registered later, so the
	// pool is still open when it executes. Using `defer pool.Close()` here
	// would silently skip every DELETE and leak rows into the test database.
	t.Cleanup(pool.Close)

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	const issuer = "https://idp.integration.invalid"
	subject := "integration-" + hex.EncodeToString(nonce)
	const tenantID = "platform"
	name := federatedPrincipalName(issuer, subject)

	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM admin_sessions WHERE principal_id IN
			(SELECT principal_id FROM federated_identities WHERE issuer=$1 AND subject=$2)`, issuer, subject)
		_, _ = pool.Exec(ctx, `DELETE FROM federated_identities WHERE issuer=$1 AND subject=$2`, issuer, subject)
		_, _ = pool.Exec(ctx, `DELETE FROM principals WHERE name=$1`, name)
	}
	cleanup()
	t.Cleanup(cleanup)

	repo := NewFederatedRepo(pool)
	first, err := repo.ProvisionIdentity(ctx, issuer, subject,
		"alice@example.invalid", "Alice", tenantID, "tenant_admin",
		map[string]any{"source": "oidc", "display_name": "Alice", "email": "alice@example.invalid"})
	if err != nil {
		t.Fatalf("first ProvisionIdentity: %v", err)
	}
	if first.ID == "" || first.Name != name {
		t.Fatalf("unexpected principal: %#v", first)
	}
	if got := first.Metadata["role"]; got != "tenant_admin" {
		t.Fatalf("default role on first login = %v, want tenant_admin", got)
	}
	var storedEmail, storedName string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(email,''),COALESCE(display_name,'')
		FROM federated_identities WHERE issuer=$1 AND subject=$2`, issuer, subject).
		Scan(&storedEmail, &storedName); err != nil {
		t.Fatal(err)
	}
	if storedEmail != "alice@example.invalid" || storedName != "Alice" {
		t.Fatalf("federated profile = %q / %q", storedEmail, storedName)
	}

	// An administrator changes the role inside Warden; the next IdP login must
	// not silently overwrite it with the configured default.
	if _, err := pool.Exec(ctx,
		`UPDATE principals SET metadata = metadata || '{"role":"user"}'::jsonb WHERE id=$1::uuid`,
		first.ID); err != nil {
		t.Fatal(err)
	}

	second, err := repo.ProvisionIdentity(ctx, issuer, subject,
		"bob@example.invalid", "Bob", tenantID, "platform_admin",
		map[string]any{"source": "oidc", "display_name": "Bob", "email": "bob@example.invalid", "avatar_url": "https://idp.invalid/a.png"})
	if err != nil {
		t.Fatalf("second ProvisionIdentity: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("principal changed on repeat login: %s -> %s", first.ID, second.ID)
	}
	if got := second.Metadata["role"]; got != "user" {
		t.Fatalf("admin-assigned role was overwritten: %v", got)
	}
	if got := second.Metadata["display_name"]; got != "Bob" {
		t.Fatalf("display attributes not refreshed: %v", got)
	}
	if got := second.Metadata["avatar_url"]; got != "https://idp.invalid/a.png" {
		t.Fatalf("avatar not refreshed: %v", got)
	}

	// Identities provisioned before role mapping existed have no role at all.
	// A later login with a configured default must backfill it, which is what
	// makes an existing SSO account pick up its new permissions.
	if _, err := pool.Exec(ctx,
		`UPDATE principals SET metadata = metadata - 'role' WHERE id=$1::uuid`, first.ID); err != nil {
		t.Fatal(err)
	}
	third, err := repo.ProvisionIdentity(ctx, issuer, subject,
		"bob@example.invalid", "Bob", tenantID, "platform_admin",
		map[string]any{"source": "oidc", "display_name": "Bob"})
	if err != nil {
		t.Fatalf("third ProvisionIdentity: %v", err)
	}
	if got := third.Metadata["role"]; got != "platform_admin" {
		t.Fatalf("missing role was not backfilled: %v", got)
	}

	var identityRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM federated_identities WHERE issuer=$1 AND subject=$2`, issuer, subject).Scan(&identityRows); err != nil {
		t.Fatal(err)
	}
	if identityRows != 1 {
		t.Fatalf("identity rows = %d, want 1 (subject uniqueness)", identityRows)
	}
}
