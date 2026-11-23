package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestExecutionIdempotencyConcurrentDuplicate 验证并发相同幂等键不返回 500：
// 两个请求同时读查均未命中、都进入 CreateTx，唯一约束只放行一个，另一个捕获
// 23505（ON CONFLICT DO NOTHING）后回查幂等返回已有执行，而非 500。
func TestExecutionIdempotencyConcurrentDuplicate(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()

	vaultID := testResourceID(t, pool, `SELECT id FROM resources WHERE resource_type='credential_vault' AND external_id='warden/control-plane' LIMIT 1`)
	key := "idem-concurrent-001"
	body := `{"resourceId":"` + vaultID + `","action":"credential.create","actionVersion":1,` +
		`"parameters":{"name":"idem-token","kind":"token","value":"secret","purpose":"test"},"reason":"idempotency test"}`

	start := make(chan struct{})
	results := make([]int, 2)
	bodies := make([]string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/executions", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+testAdminAPIKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key)
			resp := httptest.NewRecorder()
			mux.ServeHTTP(resp, req)
			results[idx] = resp.Code
			bodies[idx] = resp.Body.String()
		}(i)
	}
	close(start)
	wg.Wait()

	for i, code := range results {
		if code == http.StatusInternalServerError {
			t.Fatalf("并发幂等请求返回 500: codes=%v body=%s", results, bodies[i])
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM executions WHERE idempotency_key=$1`, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("并发幂等键创建了 %d 条 execution，期望 1（幂等去重失败）", count)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM executions WHERE idempotency_key=$1`, key)
	})
}