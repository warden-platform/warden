package control

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---
//
// Regression: expirePendingApprovals must tell the benign "approver won the
// race" case apart from real database failures. Real failures must be logged
// (previously every error was silently swallowed as a race, so a persistent
// outage stopped expiration without any trace).

// seedExpiredApproval inserts a WAITING_APPROVAL execution plus its PENDING
// access request whose expires_at is already in the past.
func seedExpiredApproval(t *testing.T, pool *pgxpool.Pool, traceID string) (requestID, executionID string) {
	t.Helper()
	ctx := context.Background()
	resourceID := insertDiagnosticResource(t, pool, "expire-"+traceID, "default", "staging")
	if err := pool.QueryRow(ctx, `INSERT INTO executions
		(tenant_id, principal_id, resource_id, action_id, parameters, parameter_hash, status, trace_id, connector_id)
		VALUES ('platform', '00000000-0000-0000-0000-000000000003', $1, 'a0000000-0000-0000-0000-000000000001',
		        '{}'::jsonb, 'expire-test-hash', 'WAITING_APPROVAL', $2, (SELECT connector_id FROM resources WHERE id=$1))
		RETURNING id`, resourceID, traceID).Scan(&executionID); err != nil {
		t.Fatalf("insert waiting execution: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_requests
		(tenant_id, principal_id, resource_id, action_id, execution_id, parameters, parameter_hash, reason, status, expires_at)
		VALUES ('platform', '00000000-0000-0000-0000-000000000003', $1, 'a0000000-0000-0000-0000-000000000001', $2,
		        '{}'::jsonb, 'expire-test-hash', 'expiration test', 'PENDING', now()-interval '1 hour')
		RETURNING id`, resourceID, executionID).Scan(&requestID); err != nil {
		t.Fatalf("insert expired pending request: %v", err)
	}
	return requestID, executionID
}

func TestExpirePendingApprovalsHappyPath(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	requestID, executionID := seedExpiredApproval(t, pool, "trace-expire-happy")

	if err := h.expirePendingApprovals(context.Background()); err != nil {
		t.Fatalf("expirePendingApprovals: %v", err)
	}

	var requestStatus, execStatus, execErrorCode string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM access_requests WHERE id=$1`, requestID).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT status, COALESCE(error_code,'') FROM executions WHERE id=$1`, executionID).Scan(&execStatus, &execErrorCode); err != nil {
		t.Fatal(err)
	}
	if requestStatus != string(domain.RequestExpired) || execStatus != string(domain.ExecExpired) || execErrorCode != "APPROVAL_EXPIRED" {
		t.Fatalf("request=%s execution=%s/%s, want EXPIRED/EXPIRED/APPROVAL_EXPIRED", requestStatus, execStatus, execErrorCode)
	}
	var auditCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action='access_request.expired' AND target_id=$1`, requestID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("expired audit events = %d, want 1", auditCount)
	}
}

func TestUpdateStatusTxRaceIsSentinel(t *testing.T) {
	pool := newTestDatabasePool(t)
	requestID, _ := seedExpiredApproval(t, pool, "trace-expire-race")
	ctx := domain.WithTenantID(context.Background(), "platform")
	repo := postgres.NewRequestRepo(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// Another writer already transitioned the request: from=PENDING matches
	// no rows. This must surface as the sentinel, not a generic error.
	if _, err := tx.Exec(ctx, `UPDATE access_requests SET status='APPROVED' WHERE id=$1`, requestID); err != nil {
		t.Fatal(err)
	}
	err = repo.UpdateStatusTx(ctx, tx, requestID, string(domain.RequestPending), string(domain.RequestExpired), "", nil)
	if !errors.Is(err, postgres.ErrRequestStateChanged) {
		t.Fatalf("race error = %v, want ErrRequestStateChanged", err)
	}
}

func TestExpirePendingApprovalsLogsRealErrors(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	ctx := context.Background()

	// Force genuine UPDATE failures with raising triggers, one table at a
	// time, and assert the worker logs instead of silently swallowing.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION expire_test_block() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'forced failure'; END $$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}

	cases := []struct {
		name    string
		table   string
		wantLog string
	}{
		{"request update fails", "access_requests", "update request status failed"},
		{"execution transition fails", "executions", "transition execution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requestID, _ := seedExpiredApproval(t, pool, "trace-expire-err-"+tc.table)
			if _, err := pool.Exec(ctx, `CREATE TRIGGER expire_test_block_trg BEFORE UPDATE ON `+tc.table+
				` FOR EACH ROW EXECUTE FUNCTION expire_test_block()`); err != nil {
				t.Fatalf("create trigger: %v", err)
			}
			defer pool.Exec(ctx, `DROP TRIGGER expire_test_block_trg ON `+tc.table)

			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			if err := h.expirePendingApprovals(ctx); err != nil {
				t.Fatalf("expirePendingApprovals should continue past row errors, got: %v", err)
			}
			if !strings.Contains(buf.String(), tc.wantLog) {
				t.Fatalf("log %q missing %q", buf.String(), tc.wantLog)
			}
			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM access_requests WHERE id=$1`, requestID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != string(domain.RequestPending) {
				t.Fatalf("request status = %s, want PENDING (transaction must roll back)", status)
			}
		})
	}
}