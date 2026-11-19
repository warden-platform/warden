package connector

import (
	"strings"
	"testing"
)

func TestParseExplainJSON(t *testing.T) {
	payload := []byte(`{
		"query_block": {
			"select_id": 1,
			"cost_info": {"query_cost": "783.40"},
			"table": {
				"table_name": "orders",
				"access_type": "ALL",
				"rows_examined_per_scan": 12000,
				"using_filesort": true
			},
			"nested_loop": [{
				"table": {
					"table_name": "customers",
					"access_type": "ref",
					"rows_examined_per_scan": 5,
					"using_temporary": true
				}
			}]
		}
	}`)
	res, err := parseExplainJSON(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.EstimatedCost != 783.4 {
		t.Errorf("cost = %v, want 783.4", res.EstimatedCost)
	}
	if res.EstimatedRows != 12000 {
		t.Errorf("rows = %v, want 12000", res.EstimatedRows)
	}
	if !res.FullScan {
		t.Error("expected full scan to be detected")
	}
	if !res.UsingFilesort || !res.UsingTempTable {
		t.Errorf("expected filesort/temp flags, got filesort=%v temp=%v", res.UsingFilesort, res.UsingTempTable)
	}
	seen := map[string]bool{}
	for _, access := range res.AccessTypes {
		seen[access] = true
	}
	if !seen["ALL"] || !seen["ref"] {
		t.Errorf("access types = %v, want ALL and ref", res.AccessTypes)
	}
}

func TestEvaluateExplainCost(t *testing.T) {
	limits := DefaultExplainCostLimits()

	if verdict := EvaluateExplainCost(&ExplainResult{EstimatedRows: 200000}, limits); verdict != CostReject {
		t.Errorf("over-hard rows = %v, want reject", verdict)
	}
	if verdict := EvaluateExplainCost(&ExplainResult{EstimatedCost: 9999}, limits); verdict != CostReject {
		t.Errorf("over-hard cost = %v, want reject", verdict)
	}
	if verdict := EvaluateExplainCost(&ExplainResult{EstimatedRows: 20000}, limits); verdict != CostRequireApproval {
		t.Errorf("over-soft rows = %v, want approval", verdict)
	}
	if verdict := EvaluateExplainCost(&ExplainResult{FullScan: true}, limits); verdict != CostRequireApproval {
		t.Errorf("full scan = %v, want approval", verdict)
	}
	if verdict := EvaluateExplainCost(&ExplainResult{UsingTempTable: true}, limits); verdict != CostRequireApproval {
		t.Errorf("temp table = %v, want approval", verdict)
	}
	if verdict := EvaluateExplainCost(&ExplainResult{EstimatedRows: 100, EstimatedCost: 50}, limits); verdict != CostAllow {
		t.Errorf("cheap query = %v, want allow", verdict)
	}
}

func TestEvaluateQueryCostAllowsSmallBoundedSimpleScan(t *testing.T) {
	limit := 50
	plan := &QueryAST{
		Sources: []QuerySource{{Alias: "t", ResourceID: "table/app/user_token"}},
		Select:  []QuerySelect{{Field: &QueryField{Source: "t", Column: "user_agent"}, Alias: "user_agent"}},
		Limit:   &limit,
	}
	assessment := EvaluateQueryCost(&ExplainResult{
		EstimatedRows: 3229,
		EstimatedCost: 781.14,
		FullScan:      true,
	}, plan, DefaultExplainCostLimits())
	if assessment.Verdict != CostAllow {
		t.Fatalf("small bounded simple scan = %v (%v), want allow", assessment.Verdict, assessment.Reasons)
	}
}

func TestEvaluateQueryCostDoesNotLetLimitHideBlockingWork(t *testing.T) {
	limit := 50
	base := QueryAST{
		Sources: []QuerySource{{Alias: "t", ResourceID: "table/app/orders"}},
		Select:  []QuerySelect{{Field: &QueryField{Source: "t", Column: "created_at"}, Alias: "created_at"}},
		Limit:   &limit,
	}
	tests := []struct {
		name string
		plan QueryAST
		res  ExplainResult
	}{
		{name: "filesort", plan: base, res: ExplainResult{EstimatedRows: 3229, EstimatedCost: 781.14, UsingFilesort: true}},
		{name: "large scan", plan: base, res: ExplainResult{EstimatedRows: 20000, EstimatedCost: 400, FullScan: true}},
	}
	aggregate := base
	aggregate.Select = []QuerySelect{{Aggregate: "COUNT", Alias: "count"}}
	tests = append(tests, struct {
		name string
		plan QueryAST
		res  ExplainResult
	}{name: "aggregate", plan: aggregate, res: ExplainResult{EstimatedRows: 3229, EstimatedCost: 781.14, FullScan: true}})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assessment := EvaluateQueryCost(&test.res, &test.plan, DefaultExplainCostLimits())
			if assessment.Verdict != CostRequireApproval || len(assessment.Reasons) == 0 {
				t.Fatalf("assessment = %#v, want approval with reasons", assessment)
			}
		})
	}
}

func TestEvaluateQueryCostAllowsTinyOptimizerOperations(t *testing.T) {
	limit := 50
	plan := &QueryAST{
		Sources: []QuerySource{{Alias: "t", ResourceID: "table/app/small_lookup"}},
		Select:  []QuerySelect{{Field: &QueryField{Source: "t", Column: "name"}, Alias: "name"}},
		OrderBy: []QueryOrder{{OutputAlias: "name", Direction: "ASC"}},
		Limit:   &limit,
	}
	assessment := EvaluateQueryCost(&ExplainResult{
		EstimatedRows: 100,
		EstimatedCost: 40,
		FullScan:      true,
		UsingFilesort: true,
	}, plan, DefaultExplainCostLimits())
	if assessment.Verdict != CostAllow {
		t.Fatalf("tiny optimizer operations = %#v, want allow", assessment)
	}
}

func TestEvaluateQueryCostDoesNotLetLimitHideNearHardCost(t *testing.T) {
	limit := 1
	plan := &QueryAST{
		Sources: []QuerySource{{Alias: "t", ResourceID: "table/app/expensive"}},
		Select:  []QuerySelect{{Field: &QueryField{Source: "t", Column: "id"}, Alias: "id"}},
		Limit:   &limit,
	}
	assessment := EvaluateQueryCost(&ExplainResult{
		EstimatedRows: 9000,
		EstimatedCost: 1100,
		FullScan:      true,
	}, plan, DefaultExplainCostLimits())
	if assessment.Verdict != CostRequireApproval {
		t.Fatalf("bounded query with material cost = %#v, want require_approval", assessment)
	}
}

func TestInterpolateMySQL(t *testing.T) {
	sql := "SELECT x FROM t WHERE a = ? AND b = ?"
	got := interpolateMySQL(sql, []any{1, "o'clock"})
	want := "SELECT x FROM t WHERE a = 1 AND b = 'o\\'clock'"
	if got != want {
		t.Errorf("interpolate:\n got: %s\nwant: %s", got, want)
	}
}

func TestInterpolateMySQLSkipsBacktickQuestion(t *testing.T) {
	sql := "SELECT `?weird` FROM t WHERE a = ?"
	got := interpolateMySQL(sql, []any{5})
	if !strings.Contains(got, "`?weird`") {
		t.Errorf("backtick identifier altered: %s", got)
	}
	if !strings.Contains(got, "a = 5") {
		t.Errorf("placeholder not substituted: %s", got)
	}
}

func TestInterpolateMySQLNullAndBool(t *testing.T) {
	got := interpolateMySQL("WHERE a = ? AND b = ? AND c = ?", []any{nil, true, false})
	want := "WHERE a = NULL AND b = TRUE AND c = FALSE"
	if got != want {
		t.Errorf("interpolate:\n got: %s\nwant: %s", got, want)
	}
}
