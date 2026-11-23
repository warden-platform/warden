package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

// TestSyncResourcesSkipsOrphanOnPartialFailure 验证：一次同步中若某条资源
// 因空 name/type 被跳过或 upsert 失败，则整批跳过 orphan——否则该资源未写入
// 本次 sync_generation，会被 MarkMissingFromSync 误标 ORPHANED，资源从活跃
// 目录消失。
func TestSyncResourcesSkipsOrphanOnPartialFailure(t *testing.T) {
	pool := newTestDatabasePool(t)
	h, _ := newTestDatabaseHandler(t, pool)
	ctx := domain.WithTenantID(context.Background(), "platform")

	var connectorID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors
		(name, connector_type, status, environment, version, capabilities, last_heartbeat, tenant_id)
		VALUES ('sync-partial-test', 'linux', 'ACTIVE', 'production', '1.0.0', '[]'::jsonb, now(), 'platform')
		RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO resources
		(resource_type, external_id, name, namespace, connector_id, environment, status, tenant_id, sync_generation)
		VALUES ('systemd_service', 'existing-svc', 'existing-svc', 'host-a', $1, 'production', 'ACTIVE', 'platform', 'gen-1')`,
		connectorID); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"connectorId":%q,"syncId":"gen-2","complete":true,`+
		`"resources":[{"resourceType":"systemd_service","externalId":"existing-svc","name":"","namespace":"host-a"}]}`,
		connectorID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resources/sync", strings.NewReader(body))
	req = req.WithContext(ctx)
	resp := httptest.NewRecorder()
	h.SyncResources(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("sync code = %d, body=%s", resp.Code, resp.Body.String())
	}

	var status, gen string
	if err := pool.QueryRow(ctx, `SELECT status, sync_generation FROM resources
		WHERE connector_id=$1 AND external_id='existing-svc'`, connectorID).Scan(&status, &gen); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.ResourceActive) || gen != "gen-1" {
		t.Fatalf("existing resource 被改动/误孤儿: status=%s gen=%s（期望 ACTIVE/gen-1）", status, gen)
	}
}