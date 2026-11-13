package connector

import (
	"fmt"
	"strings"
	"testing"
)

func exampleAST() *QueryAST {
	limit := 100
	return &QueryAST{
		Version: QueryASTVersion,
		Sources: []QuerySource{{Alias: "o", ResourceID: "orders-id"}},
		Joins: []QueryJoin{{
			Type:   "INNER",
			Source: QuerySource{Alias: "c", ResourceID: "customers-id"},
			On: []QueryJoinOn{{
				Left:     QueryField{Source: "o", Column: "customer_id"},
				Operator: "eq",
				Right:    QueryField{Source: "c", Column: "id"},
			}},
		}},
		Select: []QuerySelect{
			{Field: &QueryField{Source: "c", Column: "region"}, Alias: "region"},
			{Aggregate: "COUNT", Field: &QueryField{Source: "o", Column: "id"}, Alias: "order_count"},
			{Aggregate: "SUM", Field: &QueryField{Source: "o", Column: "amount"}, Alias: "total_amount"},
		},
		Where: &QueryPredicate{And: []QueryPredicate{
			{Field: &QueryField{Source: "o", Column: "status"}, Operator: "eq", Parameter: "status"},
			{Field: &QueryField{Source: "o", Column: "created_at"}, Operator: "gte", Parameter: "startTime"},
		}},
		GroupBy: []QueryField{{Source: "c", Column: "region"}},
		Having: []QueryHaving{{
			Aggregate: "SUM", Field: QueryField{Source: "o", Column: "amount"},
			Operator: "gte", Parameter: "minimumAmount",
		}},
		OrderBy: []QueryOrder{{OutputAlias: "total_amount", Direction: "DESC"}},
		Limit:   &limit,
		Bindings: map[string]any{
			"status":        "PAID",
			"startTime":     "2026-08-01T00:00:00Z",
			"minimumAmount": 10000,
		},
	}
}

func exampleResolver() func(string) (QueryTableRef, error) {
	tables := map[string]QueryTableRef{
		"orders-id":    {Schema: "business", Table: "orders"},
		"customers-id": {Schema: "business", Table: "customers"},
	}
	return func(resourceID string) (QueryTableRef, error) {
		ref, ok := tables[resourceID]
		if !ok {
			return QueryTableRef{}, fmt.Errorf("unknown resource %q", resourceID)
		}
		return ref, nil
	}
}

func TestRenderMySQLQueryGolden(t *testing.T) {
	plan, hash, err := CanonicalizeQuery(exampleAST())
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty plan hash")
	}
	rendered, err := RenderMySQLQuery(plan, exampleAST().Bindings, exampleResolver())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	wantSQL := "SELECT `c`.`region` AS `region`, COUNT(`o`.`id`) AS `order_count`, " +
		"SUM(`o`.`amount`) AS `total_amount` FROM `business`.`orders` AS `o` " +
		"INNER JOIN `business`.`customers` AS `c` ON `o`.`customer_id` = `c`.`id` " +
		"WHERE `o`.`status` = ? AND `o`.`created_at` >= ? GROUP BY `c`.`region` " +
		"HAVING SUM(`o`.`amount`) >= ? ORDER BY `total_amount` DESC LIMIT ?"
	if rendered.SQL != wantSQL {
		t.Errorf("SQL mismatch:\n got: %s\nwant: %s", rendered.SQL, wantSQL)
	}
	wantArgs := []any{"PAID", "2026-08-01T00:00:00Z", 10000, 100}
	if len(rendered.Args) != len(wantArgs) {
		t.Fatalf("arg count = %d, want %d", len(rendered.Args), len(wantArgs))
	}
	for i := range wantArgs {
		if rendered.Args[i] != wantArgs[i] {
			t.Errorf("arg[%d] = %v, want %v", i, rendered.Args[i], wantArgs[i])
		}
	}
}

func TestCanonicalHashStableAndBindingIndependent(t *testing.T) {
	a := exampleAST()
	_, hashA, err := CanonicalizeQuery(a)
	if err != nil {
		t.Fatalf("canonicalize a: %v", err)
	}
	b := exampleAST()
	b.Bindings["minimumAmount"] = 99999
	_, hashB, err := CanonicalizeQuery(b)
	if err != nil {
		t.Fatalf("canonicalize b: %v", err)
	}
	if hashA != hashB {
		t.Errorf("plan hash changed with binding value: %s != %s", hashA, hashB)
	}
}

func TestCanonicalRejectsUnknownAlias(t *testing.T) {
	ast := exampleAST()
	ast.Where = &QueryPredicate{Field: &QueryField{Source: "ghost", Column: "id"}, Operator: "eq", Parameter: "x"}
	ast.Bindings["x"] = 1
	if _, _, err := CanonicalizeQuery(ast); err == nil {
		t.Fatal("expected unknown alias to be rejected")
	}
}

func TestCanonicalRejectsDuplicateAlias(t *testing.T) {
	ast := exampleAST()
	ast.Joins[0].Source.Alias = "o"
	if _, _, err := CanonicalizeQuery(ast); err == nil {
		t.Fatal("expected duplicate alias to be rejected")
	}
}

func TestCanonicalRejectsExcessLimit(t *testing.T) {
	ast := exampleAST()
	limit := maxQueryLimit + 1
	ast.Limit = &limit
	if _, _, err := CanonicalizeQuery(ast); err == nil {
		t.Fatal("expected oversized limit to be rejected")
	}
}

func TestCanonicalRejectsUnknownOperator(t *testing.T) {
	ast := exampleAST()
	ast.Where = &QueryPredicate{Field: &QueryField{Source: "o", Column: "status"}, Operator: "drop", Parameter: "status"}
	if _, _, err := CanonicalizeQuery(ast); err == nil {
		t.Fatal("expected unknown operator to be rejected")
	}
}

func TestCanonicalRejectsParamAndValue(t *testing.T) {
	ast := exampleAST()
	ast.Where = &QueryPredicate{Field: &QueryField{Source: "o", Column: "status"}, Operator: "eq", Parameter: "status", Value: "PAID"}
	if _, _, err := CanonicalizeQuery(ast); err == nil {
		t.Fatal("expected both parameter and value to be rejected")
	}
}

func TestCanonicalRejectsMissingBinding(t *testing.T) {
	ast := exampleAST()
	ast.Where = &QueryPredicate{Field: &QueryField{Source: "o", Column: "status"}, Operator: "eq", Parameter: "missing"}
	if _, _, err := CanonicalizeQuery(ast); err == nil {
		t.Fatal("expected missing binding to be rejected")
	}
}

func TestCanonicalRejectsDeepPredicate(t *testing.T) {
	leaf := QueryPredicate{Field: &QueryField{Source: "o", Column: "status"}, Operator: "eq", Parameter: "status"}
	var deep *QueryPredicate = &leaf
	for i := 0; i <= maxQueryPredicateDepth; i++ {
		deep = &QueryPredicate{Not: deep}
	}
	ast := exampleAST()
	ast.Where = deep
	if _, _, err := CanonicalizeQuery(ast); err == nil {
		t.Fatal("expected over-deep predicate to be rejected")
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	payload := []byte(`{"astVersion":1,"sources":[{"alias":"o","resourceId":"r"}],"select":[{"field":{"source":"o","column":"id"},"alias":"id"}],"unknown":true}`)
	if _, err := decodeQueryAST(payload); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestCanonicalRejectsMissingASTVersionClearly(t *testing.T) {
	ast := exampleAST()
	ast.Version = 0
	_, _, err := CanonicalizeQuery(ast)
	if err == nil || !strings.Contains(err.Error(), "astVersion is required") {
		t.Fatalf("unexpected missing-version error: %v", err)
	}
}

func TestRenderEscapesBacktickIdentifier(t *testing.T) {
	ast := &QueryAST{
		Version: QueryASTVersion,
		Sources: []QuerySource{{Alias: "o", ResourceID: "r"}},
		Select:  []QuerySelect{{Field: &QueryField{Source: "o", Column: "weird`col"}, Alias: "out`put"}},
	}
	plan, _, err := CanonicalizeQuery(ast)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	rendered, err := RenderMySQLQuery(plan, nil, func(string) (QueryTableRef, error) {
		return QueryTableRef{Schema: "s", Table: "t"}, nil
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rendered.SQL, "`weird``col`") || !strings.Contains(rendered.SQL, "`out``put`") {
		t.Errorf("backticks not escaped: %s", rendered.SQL)
	}
}

func TestRenderAllValuesArePlaceholders(t *testing.T) {
	plan, _, err := CanonicalizeQuery(exampleAST())
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	rendered, err := RenderMySQLQuery(plan, exampleAST().Bindings, exampleResolver())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(rendered.SQL, "PAID") || strings.Contains(rendered.SQL, "10000") {
		t.Errorf("literal value leaked into SQL: %s", rendered.SQL)
	}
}

func TestRenderCountDistinct(t *testing.T) {
	ast := &QueryAST{
		Version: QueryASTVersion,
		Sources: []QuerySource{{Alias: "o", ResourceID: "r"}},
		Select:  []QuerySelect{{Aggregate: "COUNT", Distinct: true, Field: &QueryField{Source: "o", Column: "id"}, Alias: "n"}},
	}
	plan, _, err := CanonicalizeQuery(ast)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	rendered, err := RenderMySQLQuery(plan, nil, func(string) (QueryTableRef, error) {
		return QueryTableRef{Schema: "s", Table: "t"}, nil
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rendered.SQL, "COUNT(DISTINCT `o`.`id`)") {
		t.Errorf("missing COUNT(DISTINCT ...): %s", rendered.SQL)
	}
}
