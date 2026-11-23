package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

// TestPollTasksSigningKeyFailureDoesNotClaim 验证：连接器任务签名密钥解密失败
// 在 claim 之前被拦截，可 claim 的 QUEUED 执行不被 claim（仍 QUEUED）。
//
// 旧代码先 claim（QUEUED→DISPATCHED 且 use_count+1）再逐任务解密签名，解密
// 失败 return 500 后任务卡 DISPATCHED、授权已消耗，最终被 ExpireDispatchLeases
// 记为 UNKNOWN（任务丢失+授权浪费）。修复后凭证损坏在 claim 前 fail-closed。
func TestPollTasksSigningKeyFailureDoesNotClaim(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")

	// 损坏的 task_key_ciphertext（非有效密文，Decrypt 必失败）。
	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors
		(name, connector_type, status, environment, version, capabilities, last_heartbeat, tenant_id, task_key_ciphertext)
		VALUES ('task-claim-test', 'kubernetes', 'ACTIVE', 'production', '1.0.0', '[]'::jsonb, now(), 'platform', $1)
		RETURNING id`, []byte("not-a-valid-ciphertext")).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	var resourceID, actionID, grantID, executionID string
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,tenant_id)
		VALUES ('deployment','claim-ext','r',$1,'platform') RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM action_definitions WHERE tenant_id='platform' LIMIT 1`).Scan(&actionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_grants
		(principal_id, resource_scope, action_scope, parameter_hash, scope_hash, max_uses, use_count, ttl_seconds, expires_at, status, tenant_id)
		VALUES ($1,'{}','{}','h','h',1,0,900,now()+interval '1 hour','ACTIVE','platform') RETURNING id`,
		adminPrincipalID).Scan(&grantID); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO executions
		(principal_id,resource_id,action_id,parameters,parameter_hash,trace_id,connector_id,tenant_id,status,executor_type,grant_id)
		VALUES ($1,$2,$3,'{}','h','tr',$4,'platform','QUEUED','connector',$5) RETURNING id`,
		adminPrincipalID, resourceID, actionID, connectorID, grantID).Scan(&executionID); err != nil {
		t.Fatalf("insert execution: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM executions WHERE id=$1`, executionID)
		_, _ = pool.Exec(bg, `DELETE FROM access_grants WHERE id=$1`, grantID)
		_, _ = pool.Exec(bg, `DELETE FROM resources WHERE id=$1`, resourceID)
		_, _ = pool.Exec(bg, `DELETE FROM connectors WHERE id=$1`, connectorID)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connectors/"+connectorID+"/tasks", nil)
	req.SetPathValue("id", connectorID)
	req = req.WithContext(ctx)
	resp := httptest.NewRecorder()
	h.PollTasks(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("解密失败应 500，got %d body=%s", resp.Code, resp.Body.String())
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM executions WHERE id=$1`, executionID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != "QUEUED" {
		t.Fatalf("签名密钥解密失败后执行被 claim：status=%s want QUEUED", st)
	}
}