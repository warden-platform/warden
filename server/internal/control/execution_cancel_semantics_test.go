package control

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestCancelExecutionHonestSemantics 回归生命周期 P1：取消不再对已派发/终态
// 执行静默返回 200。QUEUED 可取消（200→CANCELLED）；DISPATCHED 无召回通道、
// 终态已结束，均诚实返回 409 EXECUTION_NOT_CANCELABLE。
func TestCancelExecutionHonestSemantics(t *testing.T) {
	pool := newTestDatabasePool(t)
	_, mux := newTestDatabaseHandler(t, pool)
	ctx := context.Background()

	var connectorID, resourceID, actionID string
	if err := pool.QueryRow(ctx, `INSERT INTO connectors(name,connector_type,tenant_id) VALUES ('cancel-c','kubernetes','platform') RETURNING id`).Scan(&connectorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resources(resource_type,external_id,name,connector_id,tenant_id) VALUES ('deployment','cancel-ext','r',$1,'platform') RETURNING id`, connectorID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM action_definitions WHERE tenant_id='platform' LIMIT 1`).Scan(&actionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM executions WHERE resource_id=$1`, resourceID)
		_, _ = pool.Exec(bg, `DELETE FROM resources WHERE id=$1`, resourceID)
		_, _ = pool.Exec(bg, `DELETE FROM connectors WHERE id=$1`, connectorID)
	})

	seed := func(status string, dispatched bool) string {
		var id string
		if dispatched {
			if err := pool.QueryRow(ctx, `INSERT INTO executions(principal_id,resource_id,action_id,parameters,parameter_hash,trace_id,connector_id,tenant_id,status,dispatch_token,dispatch_expires_at) VALUES ($1,$2,$3,'{}','h','tr',$4,'platform',$5,gen_random_uuid(),now()+interval '1 hour') RETURNING id`, adminPrincipalID, resourceID, actionID, connectorID, status).Scan(&id); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := pool.QueryRow(ctx, `INSERT INTO executions(principal_id,resource_id,action_id,parameters,parameter_hash,trace_id,connector_id,tenant_id,status) VALUES ($1,$2,$3,'{}','h','tr',$4,'platform',$5) RETURNING id`, adminPrincipalID, resourceID, actionID, connectorID, status).Scan(&id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}

	// QUEUED → 200 取消成功。
	queued := seed("QUEUED", false)
	resp := doAdminRequest(t, mux, http.MethodPost, "/api/v1/executions/"+queued+"/cancel", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("cancel QUEUED: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM executions WHERE id=$1`, queued).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != "CANCELLED" {
		t.Fatalf("QUEUED cancel outcome status=%s want CANCELLED", st)
	}

	// DISPATCHED / 终态 → 409 EXECUTION_NOT_CANCELABLE（不再静默 200）。
	for _, c := range []struct {
		status     string
		dispatched bool
	}{
		{"DISPATCHED", true},
		{"SUCCEEDED", false},
		{"FAILED", false},
		{"CANCELLED", false},
	} {
		id := seed(c.status, c.dispatched)
		resp := doAdminRequest(t, mux, http.MethodPost, "/api/v1/executions/"+id+"/cancel", "")
		if resp.Code != http.StatusConflict {
			t.Fatalf("cancel %s: status=%d want 409, body=%s", c.status, resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), "EXECUTION_NOT_CANCELABLE") {
			t.Fatalf("cancel %s: want EXECUTION_NOT_CANCELABLE, body=%s", c.status, resp.Body.String())
		}
	}
}