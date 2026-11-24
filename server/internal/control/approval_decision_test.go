package control

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/policy"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- DB-gated integration tests (WARDEN_TEST_DATABASE_URL) ---
//
// decideApproval is the single transaction boundary for Web and DingTalk
// decisions. These tests cover the safety properties that boundary must
// guarantee: one-time grants, reject closing the execution, cross-channel
// approve/reject races, scope enforcement and tenant boundaries.

// seedDecisionFixture creates a member principal, connector, resource,
// WAITING_APPROVAL execution and its PENDING request inside one tenant.
func seedDecisionFixture(t *testing.T, pool *pgxpool.Pool, tenant, trace string) (requestID, executionID, connectorID string) {
	t.Helper()
	ctx := context.Background()
	var applicantID, resourceID string
	if err := pool.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata) VALUES ('user',$1,'ACTIVE',$2,'{}') RETURNING id`, "applicant-"+trace, tenant).Scan(&applicantID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,version,capabilities,last_heartbeat,tenant_id)
		VALUES ($1,'kubernetes','ACTIVE','staging','1.0.0','["provider.request@v1"]'::jsonb,now(),$2) RETURNING id`, "conn-"+trace, tenant).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,namespace,connector_id,environment,status,tenant_id)
		VALUES ('deployment',$1,$2,'default',$3,'staging','ACTIVE',$4) RETURNING id`, "ext-"+trace, "res-"+trace, connectorID, tenant).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO executions(tenant_id,principal_id,resource_id,action_id,parameters,parameter_hash,status,trace_id,connector_id)
		VALUES ($1,$2,$3,'a0000000-0000-0000-0000-000000000001','{}'::jsonb,'decision-hash','WAITING_APPROVAL',$4,$5) RETURNING id`,
		tenant, applicantID, resourceID, trace, connectorID).Scan(&executionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_requests(tenant_id,principal_id,resource_id,action_id,execution_id,parameters,parameter_hash,reason,status,expires_at)
		VALUES ($1,$2,$3,'a0000000-0000-0000-0000-000000000001',$4,'{}'::jsonb,'decision-hash','decision test','PENDING',now()+interval '1 hour') RETURNING id`,
		tenant, applicantID, resourceID, executionID).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	return requestID, executionID, connectorID
}

func decisionActor(t *testing.T, pool *pgxpool.Pool, tenant string, scopes ...string) *AuthenticatedIdentity {
	t.Helper()
	principal, err := postgres.NewPrincipalRepo(pool).GetByID(domain.WithTenantID(context.Background(), "platform"), adminPrincipalID)
	if err != nil || principal == nil {
		t.Fatalf("load admin principal: %v", err)
	}
	return &AuthenticatedIdentity{Principal: principal, TenantID: tenant, Scopes: scopes, Kind: "TEST"}
}

func TestDecideApprovalApproveIssuesOneTimeGrant(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	requestID, executionID, connectorID := seedDecisionFixture(t, pool, "acme", "trace-grant-once")
	ctx := domain.WithTenantID(context.Background(), "acme")
	actor := decisionActor(t, pool, "acme", "warden:approve")

	if _, err := h.decideApproval(ctx, requestID, true, actor); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var grantID, grantStatus string
	var maxUses int
	if err := pool.QueryRow(ctx, `SELECT g.id, g.status, g.max_uses FROM access_grants g WHERE g.request_id=$1`, requestID).Scan(&grantID, &grantStatus, &maxUses); err != nil {
		t.Fatalf("grant after approve: %v", err)
	}
	if grantStatus != string(domain.GrantActive) || maxUses != 1 {
		t.Fatalf("grant status=%s max_uses=%d, want ACTIVE/1", grantStatus, maxUses)
	}
	var execStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM executions WHERE id=$1`, executionID).Scan(&execStatus); err != nil {
		t.Fatal(err)
	}
	if execStatus != string(domain.ExecQueued) {
		t.Fatalf("execution status=%s, want QUEUED", execStatus)
	}

	// The connector claim consumes the single use; a second claim gets nothing.
	tasks, err := h.executions.ClaimTasks(ctx, connectorID, 10)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("first claim tasks=%d, want 1", len(tasks))
	}
	// dispatch_token 哈希落库：明文清空、存 sha256 哈希（防库只读者回放）
	var tokenHash string
	var plaintextCleared bool
	if err := pool.QueryRow(ctx, `SELECT dispatch_token IS NULL, COALESCE(dispatch_token_hash,'') FROM executions WHERE id=$1`, tasks[0].ExecutionID).Scan(&plaintextCleared, &tokenHash); err != nil {
		t.Fatal(err)
	}
	if !plaintextCleared || tokenHash == "" {
		t.Fatal("dispatch_token 明文未清空或哈希未落库")
	}
	if tokenHash != domain.HashDispatchToken(tasks[0].DispatchToken) {
		t.Fatalf("dispatch_token_hash=%s != hash(明文)", tokenHash)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM access_grants WHERE id=$1`, grantID).Scan(&grantStatus); err != nil {
		t.Fatal(err)
	}
	if grantStatus != string(domain.GrantUsed) {
		t.Fatalf("grant status after claim=%s, want USED", grantStatus)
	}
	tasks, err = h.executions.ClaimTasks(ctx, connectorID, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("second claim tasks=%d, want 0 (one-time grant)", len(tasks))
	}

	// A repeated decision is rejected.
	_, err = h.decideApproval(ctx, requestID, true, actor)
	var decisionErr *approvalDecisionError
	if !errors.As(err, &decisionErr) || decisionErr.Status != 409 || decisionErr.Code != "ALREADY_PROCESSED" {
		t.Fatalf("repeat approve err=%v, want 409 ALREADY_PROCESSED", err)
	}
}

func TestDecideApprovalRejectClosesExecution(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	requestID, executionID, _ := seedDecisionFixture(t, pool, "acme", "trace-reject-close")
	ctx := domain.WithTenantID(context.Background(), "acme")
	actor := decisionActor(t, pool, "acme", "warden:approve")

	if _, err := h.decideApproval(ctx, requestID, false, actor); err != nil {
		t.Fatalf("reject: %v", err)
	}
	var requestStatus, execStatus, execErrorCode string
	if err := pool.QueryRow(ctx, `SELECT status FROM access_requests WHERE id=$1`, requestID).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(error_code,'') FROM executions WHERE id=$1`, executionID).Scan(&execStatus, &execErrorCode); err != nil {
		t.Fatal(err)
	}
	if requestStatus != string(domain.RequestRejected) || execStatus != string(domain.ExecFailed) || execErrorCode != "APPROVAL_REJECTED" {
		t.Fatalf("request=%s execution=%s/%s, want REJECTED/FAILED/APPROVAL_REJECTED", requestStatus, execStatus, execErrorCode)
	}
	var grantCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_grants WHERE request_id=$1`, requestID).Scan(&grantCount); err != nil {
		t.Fatal(err)
	}
	if grantCount != 0 {
		t.Fatalf("reject issued %d grants, want 0", grantCount)
	}
}

func TestDecideApprovalConcurrentRace(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	actor := decisionActor(t, pool, "acme", "warden:approve")

	// approve vs reject on the same request, repeated: exactly one side may
	// win; the loser must get a 409 and the database must stay consistent.
	for round := 0; round < 10; round++ {
		requestID, executionID, _ := seedDecisionFixture(t, pool, "acme", "trace-race-"+string(rune('a'+round)))
		ctx := domain.WithTenantID(context.Background(), "acme")
		start := make(chan struct{})
		results := make([]error, 2)
		var wg sync.WaitGroup
		for i, approve := range []bool{true, false} {
			wg.Add(1)
			go func(index int, approve bool) {
				defer wg.Done()
				<-start
				_, results[index] = h.decideApproval(ctx, requestID, approve, actor)
			}(i, approve)
		}
		close(start)
		wg.Wait()

		winners := 0
		for _, err := range results {
			if err == nil {
				winners++
				continue
			}
			var decisionErr *approvalDecisionError
			if !errors.As(err, &decisionErr) || decisionErr.Status != 409 {
				t.Fatalf("round %d loser err=%v, want 409", round, err)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d winners=%d, want exactly 1", round, winners)
		}
		var requestStatus, execStatus string
		if err := pool.QueryRow(ctx, `SELECT status FROM access_requests WHERE id=$1`, requestID).Scan(&requestStatus); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM executions WHERE id=$1`, executionID).Scan(&execStatus); err != nil {
			t.Fatal(err)
		}
		switch requestStatus {
		case string(domain.RequestApproved):
			if execStatus != string(domain.ExecQueued) {
				t.Fatalf("round %d approved but execution=%s", round, execStatus)
			}
		case string(domain.RequestRejected):
			if execStatus != string(domain.ExecFailed) {
				t.Fatalf("round %d rejected but execution=%s", round, execStatus)
			}
		default:
			t.Fatalf("round %d request left in status %s", round, requestStatus)
		}
	}
}

func TestDecideApprovalGuards(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	requestID, _, _ := seedDecisionFixture(t, pool, "acme", "trace-guards")
	ctx := domain.WithTenantID(context.Background(), "acme")

	// No approve scope -> 403.
	_, err := h.decideApproval(ctx, requestID, true, decisionActor(t, pool, "acme", "warden:executions:read:self"))
	var decisionErr *approvalDecisionError
	if !errors.As(err, &decisionErr) || decisionErr.Status != 403 {
		t.Fatalf("scope guard err=%v, want 403", err)
	}

	// A request in another tenant is invisible even to a platform admin
	// principal when the context tenant does not match.
	otherRequest, _, _ := seedDecisionFixture(t, pool, "chunf", "trace-guards-chunf")
	_, err = h.decideApproval(ctx, otherRequest, true, decisionActor(t, pool, "acme", "warden:approve"))
	if !errors.As(err, &decisionErr) || decisionErr.Status != 404 {
		t.Fatalf("tenant guard err=%v, want 404", err)
	}
}

func TestEffectiveGrantTTLSeconds(t *testing.T) {
	cases := []struct {
		name           string
		action, policy int
		want           int
	}{
		{"policy tightens action timeout", 120, 45, 45},
		{"looser policy cannot extend action timeout", 30, 300, 30},
		{"missing policy cap keeps action timeout", 30, 0, 30},
		{"negative policy cap is ignored", 30, -1, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveGrantTTLSeconds(tc.action, tc.policy); got != tc.want {
				t.Fatalf("effectiveGrantTTLSeconds(%d,%d)=%d, want %d", tc.action, tc.policy, got, tc.want)
			}
		})
	}
}

type fixedDecisionProvider struct{ output *policy.Output }

func (p fixedDecisionProvider) Evaluate(context.Context, policy.Input) (*policy.Output, error) {
	return p.output, nil
}

func TestPolicyMaxDurationFromOutput(t *testing.T) {
	for name, tc := range map[string]struct {
		output map[string]any
		want   int
	}{
		"nil":         {nil, 0},
		"json number": {map[string]any{"max_duration_seconds": float64(45)}, 45},
		"int":         {map[string]any{"max_duration_seconds": 45}, 45},
		"missing":     {map[string]any{"decision": "ALLOW"}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := policyMaxDurationFromOutput(tc.output); got != tc.want {
				t.Fatalf("policyMaxDurationFromOutput(%v)=%d, want %d", tc.output, got, tc.want)
			}
		})
	}
}

// TestDecideApprovalHonorsPolicyDurationCap verifies that the async approval
// path restores the immutable policy decision's upper bound instead of using
// the action timeout alone.
func TestDecideApprovalHonorsPolicyDurationCap(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	requestID, _, _ := seedDecisionFixture(t, pool, "acme", "trace-policy-cap")
	ctx := domain.WithTenantID(context.Background(), "acme")

	var decisionID string
	if err := pool.QueryRow(ctx, `INSERT INTO policy_decisions
		(tenant_id, opa_input, opa_output, result, bundle_hash, decision_seconds)
		VALUES ('acme','{}'::jsonb,'{"max_duration_seconds":10}'::jsonb,'REQUIRE_APPROVAL','test-cap',0)
		RETURNING id`).Scan(&decisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE access_requests SET decision_id=$2 WHERE id=$1`, requestID, decisionID); err != nil {
		t.Fatal(err)
	}

	if _, err := h.decideApproval(ctx, requestID, true, decisionActor(t, pool, "acme", "warden:approve")); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// deployment.get 的种子 timeout 是 30 秒，策略上限应把它压到 10 秒。
	var ttl int
	var expiresAt time.Time
	if err := pool.QueryRow(ctx, `SELECT ttl_seconds, expires_at FROM access_grants WHERE request_id=$1`, requestID).Scan(&ttl, &expiresAt); err != nil {
		t.Fatalf("grant after approve: %v", err)
	}
	if ttl != 10 {
		t.Fatalf("grant ttl=%d, want policy cap 10", ttl)
	}
	remaining := time.Until(expiresAt)
	if remaining <= 0 || remaining > 10*time.Second {
		t.Fatalf("grant expires in %v, want <=10s", remaining)
	}
}

// TestCreateExecutionHonorsPolicyDurationCap covers the ALLOW path: the grant
// issued at creation time must also respect the policy's hard upper bound.
func TestCreateExecutionHonorsPolicyDurationCap(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, mux := newTestDatabaseHandler(t, pool)
	h.decisionProvider = fixedDecisionProvider{output: &policy.Output{
		Decision: domain.DecisionAllow, ReasonCodes: []string{"POLICY_ALLOW"},
		PolicyBundleHash: "sha256:test", MaxDurationSeconds: 10,
		CredentialMode: "KUBERNETES_TOKEN_REQUEST", ParameterConstraints: map[string]any{},
	}}
	ctx := domain.WithTenantID(context.Background(), "platform")

	var connectorID, resourceID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,status,environment,version,capabilities,last_heartbeat,tenant_id)
		VALUES ('ttl-cap-connector','kubernetes','ACTIVE','testing','1.0.0','["deployment.get@v1"]'::jsonb,now(),'platform') RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,namespace,connector_id,environment,status,tenant_id)
		VALUES ('deployment','ttl-cap-deploy','ttl-cap-deploy','default',$1,'testing','ACTIVE','platform') RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}

	resp := createExecutionForResource(t, mux, resourceID, "deployment.get", map[string]any{"name": "ttl-cap-deploy", "namespace": "default"})
	execution := parseExecutionResponse(t, resp)
	if execution.ID == "" {
		t.Fatalf("create execution failed: status=%d body=%s", resp.Code, resp.Body.String())
	}

	var ttl int
	if err := pool.QueryRow(ctx, `SELECT ttl_seconds FROM access_grants WHERE id=(SELECT grant_id FROM executions WHERE id=$1)`, execution.ID).Scan(&ttl); err != nil {
		t.Fatalf("grant for execution: %v", err)
	}
	if ttl != 10 {
		t.Fatalf("grant ttl=%d, want policy cap 10", ttl)
	}
	var capped int
	if err := pool.QueryRow(ctx, `SELECT (opa_output->>'effective_grant_ttl_seconds')::int FROM policy_decisions WHERE id=(SELECT decision_id FROM executions WHERE id=$1)`, execution.ID).Scan(&capped); err != nil {
		t.Fatalf("decision effective ttl: %v", err)
	}
	if capped != 10 {
		t.Fatalf("policy decision effective ttl=%d, want 10", capped)
	}
}