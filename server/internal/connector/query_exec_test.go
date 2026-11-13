package connector

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
	"github.com/DATA-DOG/go-sqlmock"
)

func queryExecuteParameters() map[string]any {
	ast := map[string]any{
		"astVersion": 1,
		"sources": []any{
			map[string]any{"alias": "o", "resourceId": "table/business/orders"},
		},
		"joins": []any{
			map[string]any{
				"type":   "INNER",
				"source": map[string]any{"alias": "c", "resourceId": "table/business/customers"},
				"on": []any{
					map[string]any{
						"left":     map[string]any{"source": "o", "column": "customer_id"},
						"operator": "eq",
						"right":    map[string]any{"source": "c", "column": "id"},
					},
				},
			},
		},
		"select": []any{
			map[string]any{"field": map[string]any{"source": "c", "column": "region"}, "alias": "region"},
			map[string]any{"aggregate": "COUNT", "field": map[string]any{"source": "o", "column": "id"}, "alias": "order_count"},
		},
		"groupBy":  []any{map[string]any{"source": "c", "column": "region"}},
		"limit":    100,
		"bindings": map[string]any{},
	}
	return map[string]any{"ast": ast}
}

func TestResolveQueryPlan(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowAll: true}
	qp, err := r.resolveQueryPlan(queryExecuteParameters())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if qp.planHash == "" || qp.previewHash == "" {
		t.Fatal("expected non-empty plan hash")
	}
	if !strings.Contains(qp.rendered.SQL, "FROM `business`.`orders` AS `o`") {
		t.Errorf("FROM clause missing: %s", qp.rendered.SQL)
	}
	if !strings.Contains(qp.rendered.SQL, "INNER JOIN `business`.`customers` AS `c`") {
		t.Errorf("JOIN clause missing: %s", qp.rendered.SQL)
	}
	if !strings.Contains(qp.rendered.SQL, "LIMIT ?") {
		t.Errorf("LIMIT placeholder missing: %s", qp.rendered.SQL)
	}
	if len(qp.rendered.Args) != 1 || qp.rendered.Args[0] != 100 {
		t.Errorf("args = %v, want [100]", qp.rendered.Args)
	}
}

func TestPreviewFingerprintIncludesBindings(t *testing.T) {
	runner := &DatabaseRunner{protocol: "mysql", allowAll: true}
	first := queryExecuteParameters()
	firstAST := first["ast"].(map[string]any)
	firstAST["bindings"] = map[string]any{"region": "CN"}
	second := queryExecuteParameters()
	secondAST := second["ast"].(map[string]any)
	secondAST["bindings"] = map[string]any{"region": "US"}
	firstPlan, err := runner.resolveQueryPlan(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := runner.resolveQueryPlan(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.planHash != secondPlan.planHash {
		t.Fatal("structural plan hash unexpectedly includes bindings")
	}
	if firstPlan.previewHash == secondPlan.previewHash {
		t.Fatal("previewHash must bind literal values")
	}
}

func TestResolveQueryPlanRejectsOutOfScope(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowedSchemas: map[string]struct{}{"other": {}}}
	if _, err := r.resolveQueryPlan(queryExecuteParameters()); err == nil {
		t.Fatal("expected out-of-scope source to be rejected")
	}
}

func TestResolveQueryPlanRejectsInvalidResourceID(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowAll: true}
	params := queryExecuteParameters()
	ast := params["ast"].(map[string]any)
	ast["sources"] = []any{map[string]any{"alias": "o", "resourceId": "not-a-table-id"}}
	if _, err := r.resolveQueryPlan(params); err == nil {
		t.Fatal("expected invalid resourceId to be rejected")
	}
}

func TestNormalizeLegacyQueryParametersUsesSignedTableTarget(t *testing.T) {
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "database_table", ExternalID: "table/app/user_token"},
		Parameters: map[string]any{"ast": map[string]any{
			"select": []any{map[string]any{
				"field": map[string]any{"source": "t", "column": "user_agent"},
				"alias": "user_agent",
			}},
			"orderBy": []any{},
		}},
	}
	normalized := normalizeLegacyQueryParameters(task)
	runner := &DatabaseRunner{protocol: "mysql", allowAll: true}
	plan, err := runner.resolveQueryPlan(normalized)
	if err != nil {
		t.Fatalf("legacy UI query did not normalize: %v", err)
	}
	if plan.plan.Version != QueryASTVersion || len(plan.plan.Sources) != 1 || plan.plan.Sources[0].ResourceID != "table/app/user_token" {
		t.Fatalf("unexpected normalized plan: %#v", plan.plan)
	}
	if plan.plan.Limit == nil || *plan.plan.Limit != 50 {
		t.Fatalf("legacy query limit = %v, want 50", plan.plan.Limit)
	}
}

func TestNormalizeLegacyQueryParametersPreservesExplicitVersion(t *testing.T) {
	task := domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "database_table", ExternalID: "table/app/user_token"},
		Parameters: map[string]any{"ast": map[string]any{"astVersion": 0}},
	}
	normalized := normalizeLegacyQueryParameters(task)
	ast := normalized["ast"].(map[string]any)
	if ast["astVersion"] != 0 {
		t.Fatalf("explicit invalid astVersion was overwritten: %#v", ast)
	}
}

func TestLegacyUIQueryPreviewRunsExplain(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN FORMAT=JSON SELECT `t`.`user_agent` AS `user_agent` FROM `app`.`user_token` AS `t` LIMIT 50")).
		WillReturnRows(sqlmock.NewRows([]string{"EXPLAIN"}).AddRow(`{"query_block":{"cost_info":{"query_cost":"12.5"},"table":{"access_type":"ref","rows_examined_per_scan":5}}}`))
	runner := &DatabaseRunner{protocol: "mysql", allowAll: true, db: db}
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "database_table", ExternalID: "table/app/user_token"},
		Parameters: map[string]any{"ast": map[string]any{
			"select": []any{map[string]any{
				"field": map[string]any{"source": "t", "column": "user_agent"},
				"alias": "user_agent",
			}},
			"orderBy": []any{},
		}},
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
	result, _, err := runner.queryPreview(context.Background(), task)
	if err != nil {
		t.Fatalf("legacy UI preview failed: %v", err)
	}
	if result["verdict"] != string(CostAllow) || result["limit"] != 50 || result["previewHash"] == "" {
		t.Fatalf("unexpected preview result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryExecuteRequiresApprovalGrantForCostVerdict(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runner := &DatabaseRunner{protocol: "mysql", allowAll: true, db: db}
	task := simpleQueryExecuteTask(t, "")
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN FORMAT=JSON SELECT `t`.`user_agent` AS `user_agent` FROM `app`.`user_token` AS `t` LIMIT 50")).
		WillReturnRows(sqlmock.NewRows([]string{"EXPLAIN"}).AddRow(`{"query_block":{"cost_info":{"query_cost":"900"},"table":{"access_type":"ALL","rows_examined_per_scan":20000}}}`))
	result, _, err := runner.queryExecute(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_APPROVAL_REQUIRED") {
		t.Fatalf("unapproved costly query error = %v, want DATABASE_APPROVAL_REQUIRED", err)
	}
	var approvalRequired *queryApprovalRequiredError
	if !errors.As(err, &approvalRequired) || approvalRequired.result["verdict"] != string(CostRequireApproval) || result != nil {
		t.Fatalf("unapproved cost escalation = %#v, direct result %#v", approvalRequired, result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryExecuteAcceptsApprovalBackedGrant(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runner := &DatabaseRunner{protocol: "mysql", allowAll: true, db: db}
	task := simpleQueryExecuteTask(t, "request-approved")
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN FORMAT=JSON SELECT `t`.`user_agent` AS `user_agent` FROM `app`.`user_token` AS `t` LIMIT 50")).
		WillReturnRows(sqlmock.NewRows([]string{"EXPLAIN"}).AddRow(`{"query_block":{"cost_info":{"query_cost":"900"},"table":{"access_type":"ALL","rows_examined_per_scan":20000}}}`))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `t`.`user_agent` AS `user_agent` FROM `app`.`user_token` AS `t` LIMIT ?")).
		WithArgs(50).
		WillReturnRows(sqlmock.NewRows([]string{"user_agent"}).AddRow("approved-client"))
	result, _, err := runner.queryExecute(context.Background(), task)
	if err != nil {
		t.Fatalf("approved costly query failed: %v", err)
	}
	if result["rowCount"] != 1 || result["previewHash"] == "" {
		t.Fatalf("unexpected approved result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func simpleQueryExecuteTask(t *testing.T, requestID string) domain.TaskEnvelope {
	t.Helper()
	parameters := map[string]any{"ast": map[string]any{
		"astVersion": QueryASTVersion,
		"sources":    []any{map[string]any{"alias": "t", "resourceId": "table/app/user_token"}},
		"select": []any{map[string]any{
			"field": map[string]any{"source": "t", "column": "user_agent"},
			"alias": "user_agent",
		}},
		"orderBy":  []any{},
		"bindings": map[string]any{},
		"limit":    50,
	}}
	runner := &DatabaseRunner{protocol: "mysql", allowAll: true}
	plan, err := runner.resolveQueryPlan(parameters)
	if err != nil {
		t.Fatal(err)
	}
	parameters["previewHash"] = plan.previewHash
	if requestID != "" {
		parameters[domain.QueryApprovalContextParameter] = map[string]any{"requestId": requestID}
	}
	return domain.TaskEnvelope{
		Resource:   domain.TaskResource{Type: "database_table", ExternalID: "table/app/user_token"},
		Parameters: parameters,
		Grant:      domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func TestQueryResultLimit(t *testing.T) {
	if got := queryResultLimit(&QueryAST{}); got != maxDatabaseQueryRows {
		t.Errorf("default limit = %d, want %d", got, maxDatabaseQueryRows)
	}
	limit := 250
	if got := queryResultLimit(&QueryAST{Limit: &limit}); got != 250 {
		t.Errorf("limit = %d, want 250", got)
	}
}

func TestRequiredInt64(t *testing.T) {
	for _, value := range []any{float64(3), json.Number("3"), 3, int64(3)} {
		if got, err := requiredInt64(map[string]any{"v": value}, "v"); err != nil || got != 3 {
			t.Errorf("requiredInt64(%v) = %d, %v", value, got, err)
		}
	}
	for _, value := range []any{float64(0), float64(2.5), json.Number("0"), "3", nil} {
		if _, err := requiredInt64(map[string]any{"v": value}, "v"); err == nil {
			t.Errorf("expected %v to be rejected", value)
		}
	}
	if _, err := requiredInt64(map[string]any{}, "v"); err == nil {
		t.Error("expected missing key to be rejected")
	}
}
