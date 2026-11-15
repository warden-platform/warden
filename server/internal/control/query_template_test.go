package control

import (
	"encoding/json"
	"testing"

	"gitee.com/pam-platform/warden/server/internal/connector"
)

func TestBindImportedTablesBareTable(t *testing.T) {
	imported, err := connector.ImportQuerySQL("SELECT order_no FROM orders WHERE status = :status")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	ids, err := bindImportedTables("schema/shop", imported)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got := imported.AST.Sources[0].ResourceID; got != "table/shop/orders" {
		t.Errorf("source resourceId = %q, want table/shop/orders", got)
	}
	if len(ids) != 1 || ids[0] != "table/shop/orders" {
		t.Errorf("resource ids = %v", ids)
	}
}

func TestBindImportedTablesQualifiedKeepsSchema(t *testing.T) {
	imported, err := connector.ImportQuerySQL("SELECT o.id FROM shop.orders o JOIN shop.payments p ON p.order_id = o.id")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	ids, err := bindImportedTables("schema/other", imported)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got := imported.AST.Sources[0].ResourceID; got != "table/shop/orders" {
		t.Errorf("source resourceId = %q, want table/shop/orders", got)
	}
	if got := imported.AST.Joins[0].Source.ResourceID; got != "table/shop/payments" {
		t.Errorf("join resourceId = %q, want table/shop/payments", got)
	}
	if len(ids) != 2 {
		t.Errorf("resource ids = %v, want 2 entries", ids)
	}
}

func TestBindImportedTablesRequiresSchema(t *testing.T) {
	imported, err := connector.ImportQuerySQL("SELECT order_no FROM orders")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := bindImportedTables("schema/", imported); err == nil {
		t.Error("expected bare table with empty schema to be rejected")
	}
}

func TestParseSchemaExternalID(t *testing.T) {
	if got, err := parseSchemaExternalID("schema/shop"); err != nil || got != "shop" {
		t.Errorf("parseSchemaExternalID(schema/shop) = %q, %v", got, err)
	}
	if got, err := parseSchemaExternalID("schema/shop%20orders"); err != nil || got != "shop orders" {
		t.Errorf("escaped schema = %q, %v", got, err)
	}
	for _, bad := range []string{"", "table/orders", "schema", "schema/", "schema/shop%2Forders"} {
		if _, err := parseSchemaExternalID(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestRequiredTemplateVersion(t *testing.T) {
	for _, value := range []any{float64(3), json.Number("3"), 3, int64(3)} {
		if got, err := requiredTemplateVersion(map[string]any{"v": value}, "v"); err != nil || got != 3 {
			t.Errorf("requiredTemplateVersion(%v) = %d, %v", value, got, err)
		}
	}
	for _, value := range []any{float64(0), float64(2.5), "3", nil} {
		if _, err := requiredTemplateVersion(map[string]any{"v": value}, "v"); err == nil {
			t.Errorf("expected %v to be rejected", value)
		}
	}
	if _, err := requiredTemplateVersion(map[string]any{}, "v"); err == nil {
		t.Error("expected missing key to be rejected")
	}
}

func TestRequiredTemplateString(t *testing.T) {
	if got, err := requiredTemplateString(map[string]any{"v": "monthly-summary"}, "v"); err != nil || got != "monthly-summary" {
		t.Errorf("requiredTemplateString = %q, %v", got, err)
	}
	for _, value := range []any{"", 3, nil} {
		if _, err := requiredTemplateString(map[string]any{"v": value}, "v"); err == nil {
			t.Errorf("expected %v to be rejected", value)
		}
	}
}

func TestParseTableResourceID(t *testing.T) {
	if s, tbl, ok := parseTableResourceID("table/shop/orders"); !ok || s != "shop" || tbl != "orders" {
		t.Errorf("parseTableResourceID(table/shop/orders) = %q, %q, %v", s, tbl, ok)
	}
	if s, tbl, ok := parseTableResourceID("table/shop%20orders/foo%20bar"); !ok || s != "shop orders" || tbl != "foo bar" {
		t.Errorf("escaped = %q, %q, %v", s, tbl, ok)
	}
	for _, bad := range []string{"", "orders", "table/orders", "table//orders", "schema/orders"} {
		if _, _, ok := parseTableResourceID(bad); ok {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestTemplateReferencesTable(t *testing.T) {
	ast := map[string]any{
		"sources": []any{map[string]any{"alias": "o", "resourceId": "table/shop/orders"}},
		"joins":   []any{map[string]any{"type": "LEFT", "source": map[string]any{"alias": "p", "resourceId": "table/shop/payments"}}},
	}
	if !templateReferencesTable(ast, "shop", "orders") {
		t.Error("expected orders to be referenced")
	}
	if !templateReferencesTable(ast, "shop", "payments") {
		t.Error("expected payments to be referenced")
	}
	if templateReferencesTable(ast, "shop", "other") {
		t.Error("did not expect other to be referenced")
	}
}
