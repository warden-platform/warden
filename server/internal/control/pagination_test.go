package control

import (
	"context"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---
//
// Pagination regression: list endpoints must honor limit/offset instead of a
// hardcoded 50-row fetch, and offset must address the same ordered view.

func TestListExecutionsPageLimitOffset(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")

	resourceID := insertDiagnosticResource(t, pool, "page-app", "default", "staging")
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO executions
			(tenant_id, principal_id, resource_id, action_id, parameters, parameter_hash, status, trace_id, connector_id, created_at)
			VALUES ('platform', '00000000-0000-0000-0000-000000000003', $1, 'a0000000-0000-0000-0000-000000000001',
			        '{}'::jsonb, 'page-hash', 'SUCCEEDED', $2, (SELECT connector_id FROM resources WHERE id=$1), now() - $3::interval)
			RETURNING id`, resourceID, "trace-page-"+string(rune('a'+i)), time.Duration(3-i)*time.Minute).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// created_at DESC order: ids[2] (newest), ids[1], ids[0] (oldest).
	identity := &AuthenticatedIdentity{
		Principal:     &domain.Principal{ID: adminPrincipalID, Kind: domain.PrincipalKindUser, TenantID: "platform"},
		TenantID:      "platform",
		IsPlatformAdmin: true,
		Scopes:        []string{"warden:admin"},
	}

	page, err := h.listExecutionsPage(ctx, identity, "", "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != ids[2] || page[1].ID != ids[1] {
		t.Fatalf("first page wrong: got %d rows", len(page))
	}
	page, err = h.listExecutionsPage(ctx, identity, "", "", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != ids[1] || page[1].ID != ids[0] {
		t.Fatalf("offset page wrong: got %d rows", len(page))
	}
	page, err = h.listExecutionsPage(ctx, identity, "", "", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 {
		t.Fatalf("past-end page = %d rows, want 0", len(page))
	}
}