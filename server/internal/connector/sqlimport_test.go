package connector

import "testing"

func TestImportSimpleQuery(t *testing.T) {
	got, err := ImportQuerySQL("SELECT order_no, amount FROM orders WHERE status = :status AND amount > :min LIMIT 50")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	ast := got.AST
	if len(ast.Sources) != 1 || ast.Sources[0].Alias != "orders" {
		t.Fatalf("sources = %+v", ast.Sources)
	}
	if len(ast.Select) != 2 {
		t.Fatalf("select len = %d", len(ast.Select))
	}
	if ast.Select[0].Field == nil || ast.Select[0].Field.Source != "orders" || ast.Select[0].Field.Column != "order_no" {
		t.Errorf("select[0] = %+v", ast.Select[0])
	}
	if ast.Where == nil || ast.Where.And == nil || len(ast.Where.And) != 2 {
		t.Fatalf("where = %+v", ast.Where)
	}
	if ast.Where.And[1].Field.Column != "amount" || ast.Where.And[1].Operator != "gt" || ast.Where.And[1].Parameter != "min" {
		t.Errorf("where[1] = %+v", ast.Where.And[1])
	}
	if ast.Limit == nil || *ast.Limit != 50 {
		t.Errorf("limit = %v", ast.Limit)
	}
	if len(got.Tables) != 1 || got.Tables[0].Table != "orders" {
		t.Errorf("tables = %+v", got.Tables)
	}
}

func TestImportJoinAggregateQuery(t *testing.T) {
	sql := `SELECT o.order_no, SUM(p.amount) AS total
FROM shop.orders o
LEFT JOIN shop.payments p ON p.order_id = o.id
WHERE o.created_at >= :start
GROUP BY o.order_no
HAVING SUM(p.amount) > :min
ORDER BY total DESC
LIMIT 100`
	got, err := ImportQuerySQL(sql)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	ast := got.AST
	if len(ast.Sources) != 1 || ast.Sources[0].ResourceID != "table/shop/orders" {
		t.Errorf("sources = %+v", ast.Sources)
	}
	if len(ast.Joins) != 1 || ast.Joins[0].Type != "LEFT" || ast.Joins[0].Source.ResourceID != "table/shop/payments" {
		t.Fatalf("joins = %+v", ast.Joins)
	}
	if len(ast.Joins[0].On) != 1 || ast.Joins[0].On[0].Left.Column != "order_id" || ast.Joins[0].On[0].Right.Column != "id" {
		t.Errorf("on = %+v", ast.Joins[0].On)
	}
	if ast.Select[1].Aggregate != "SUM" || ast.Select[1].Alias != "total" {
		t.Errorf("select[1] = %+v", ast.Select[1])
	}
	if len(ast.GroupBy) != 1 || ast.GroupBy[0].Column != "order_no" {
		t.Errorf("groupBy = %+v", ast.GroupBy)
	}
	if len(ast.Having) != 1 || ast.Having[0].Aggregate != "SUM" || ast.Having[0].Operator != "gt" {
		t.Errorf("having = %+v", ast.Having)
	}
	if len(ast.OrderBy) != 1 || ast.OrderBy[0].OutputAlias != "total" || ast.OrderBy[0].Direction != "DESC" {
		t.Errorf("orderBy = %+v", ast.OrderBy)
	}
	if len(got.Tables) != 2 {
		t.Errorf("tables = %+v", got.Tables)
	}
}

func TestImportCountDistinct(t *testing.T) {
	got, err := ImportQuerySQL("SELECT COUNT(*), COUNT(DISTINCT email) AS c FROM users u")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got.AST.Select[0].Aggregate != "COUNT" || got.AST.Select[0].Field != nil {
		t.Errorf("count(*) = %+v", got.AST.Select[0])
	}
	if got.AST.Select[1].Aggregate != "COUNT" || !got.AST.Select[1].Distinct || got.AST.Select[1].Field.Column != "email" {
		t.Errorf("count(distinct) = %+v", got.AST.Select[1])
	}
}

func TestImportInList(t *testing.T) {
	got, err := ImportQuerySQL("SELECT id FROM orders WHERE status IN ('PAID', 'SHIPPED') AND id IN :ids")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	w := got.AST.Where
	if w.And[0].Operator != "in" {
		t.Fatalf("where[0] = %+v", w.And[0])
	}
	values, ok := w.And[0].Value.([]any)
	if !ok || len(values) != 2 {
		t.Errorf("in values = %v", w.And[0].Value)
	}
	if w.And[1].Parameter != "ids" {
		t.Errorf("in param = %+v", w.And[1])
	}
}

func TestImportRejections(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"multi-statement", "SELECT 1; SELECT 2"},
		{"dml", "DELETE FROM orders WHERE id = 1"},
		{"select star", "SELECT * FROM orders"},
		{"sleep", "SELECT id FROM orders WHERE SLEEP(5) = 0"},
		{"benchmark", "SELECT BENCHMARK(1,1) FROM orders"},
		{"load_file", "SELECT LOAD_FILE('/etc/passwd') FROM orders"},
		{"right join", "SELECT a.id FROM t1 a RIGHT JOIN t2 b ON b.id = a.id"},
		{"union", "SELECT id FROM t1 UNION SELECT id FROM t2"},
		{"user variable", "SELECT id FROM orders WHERE id = @x"},
		{"system schema", "SELECT id FROM mysql.user"},
		{"into outfile", "SELECT id INTO OUTFILE '/tmp/x' FROM orders"},
		{"limit offset", "SELECT id FROM orders LIMIT 10 OFFSET 5"},
		{"subquery", "SELECT id FROM orders WHERE id IN (SELECT id FROM other)"},
		{"unknown function", "SELECT NOW() FROM orders"},
	}
	for _, tc := range cases {
		if _, err := ImportQuerySQL(tc.sql); err == nil {
			t.Errorf("%s: expected rejection for %q", tc.name, tc.sql)
		}
	}
}

func TestImportAmbiguousColumn(t *testing.T) {
	_, err := ImportQuerySQL("SELECT id FROM a JOIN b ON b.a_id = a.id WHERE status = 'x'")
	if err == nil {
		t.Error("expected bare column in multi-table query to be rejected")
	}
}
