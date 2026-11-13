package connector

import (
	"strings"
	"testing"
)

func TestValidateColumnType(t *testing.T) {
	valid := []ColumnTypeSpec{
		{Kind: "BIGINT", Unsigned: true},
		{Kind: "VARCHAR", Length: 64},
		{Kind: "DECIMAL", Precision: 18, Scale: 2},
		{Kind: "DATETIME", Precision: 3},
		{Kind: "BOOLEAN"},
		{Kind: "JSON"},
	}
	for _, spec := range valid {
		if err := validateColumnType(spec); err != nil {
			t.Errorf("validate %+v: %v", spec, err)
		}
	}
	invalid := []ColumnTypeSpec{
		{Kind: "VARCHAR"},                  // missing length
		{Kind: "VARCHAR", Length: 0},       // zero length
		{Kind: "DECIMAL", Precision: 0},    // missing precision
		{Kind: "DECIMAL", Precision: 10, Scale: 11}, // scale > precision
		{Kind: "DATETIME", Precision: 9},   // precision out of range
		{Kind: "HACK"},
	}
	for _, spec := range invalid {
		if err := validateColumnType(spec); err == nil {
			t.Errorf("expected %+v to be rejected", spec)
		}
	}
}

func TestRenderColumnType(t *testing.T) {
	cases := map[ColumnTypeSpec]string{
		{Kind: "BIGINT", Unsigned: true}:  "BIGINT UNSIGNED",
		{Kind: "VARCHAR", Length: 64}:     "VARCHAR(64)",
		{Kind: "DECIMAL", Precision: 18, Scale: 2}: "DECIMAL(18,2)",
		{Kind: "DATETIME", Precision: 3}:  "DATETIME(3)",
		{Kind: "BOOLEAN"}:                 "BOOLEAN",
	}
	for spec, want := range cases {
		if got := renderColumnType(spec); got != want {
			t.Errorf("render %+v = %q, want %q", spec, got, want)
		}
	}
}

func TestNormalizeColumnType(t *testing.T) {
	if got := normalizeColumnType("BOOLEAN"); got != "TINYINT(1)" {
		t.Errorf("BOOLEAN -> %q", got)
	}
	if got := normalizeColumnType("bigint unsigned"); got != "BIGINT UNSIGNED" {
		t.Errorf("bigint unsigned -> %q", got)
	}
	if got := normalizeColumnType("varchar(64)"); got != "VARCHAR(64)" {
		t.Errorf("varchar(64) -> %q", got)
	}
}

func TestCompileSchema(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowCreateSchemas: true}
	ddl, fingerprint, err := r.compileSchema(SchemaCreateSpec{Name: "svc_order", CharacterSet: "utf8mb4"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(ddl, "CREATE DATABASE `svc_order`") {
		t.Errorf("DDL missing name: %s", ddl)
	}
	if !strings.Contains(ddl, "CHARACTER SET utf8mb4") {
		t.Errorf("DDL missing charset: %s", ddl)
	}
	if fingerprint == "" {
		t.Error("empty fingerprint")
	}
}

func TestCompileSchemaRejectsReserved(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowCreateSchemas: true}
	if _, _, err := r.compileSchema(SchemaCreateSpec{Name: "mysql"}); err == nil {
		t.Error("expected reserved name to be rejected")
	}
}

func TestCompileSchemaRejectsWithoutPermission(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql"}
	if _, _, err := r.compileSchema(SchemaCreateSpec{Name: "svc_order"}); err == nil {
		t.Error("expected disabled create to be rejected")
	}
}

func TestCompileTable(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowCreateSchemas: true}
	spec := TableCreateSpec{
		Name: "orders",
		Columns: []TableColumnSpec{
			{Name: "id", Type: ColumnTypeSpec{Kind: "BIGINT", Unsigned: true}, AutoIncrement: true},
			{Name: "order_no", Type: ColumnTypeSpec{Kind: "VARCHAR", Length: 64}},
			{Name: "amount", Type: ColumnTypeSpec{Kind: "DECIMAL", Precision: 18, Scale: 2}},
			{Name: "created_at", Type: ColumnTypeSpec{Kind: "DATETIME", Precision: 3}, Default: &ColumnDefault{Kind: "CURRENT_TIMESTAMP"}},
		},
		PrimaryKey:        []string{"id"},
		UniqueConstraints: []UniqueConstraint{{Name: "uk_orders_order_no", Columns: []string{"order_no"}}},
		Indexes:           []IndexSpec{{Name: "idx_orders_created_at", Columns: []IndexColumn{{Name: "created_at", Direction: "DESC"}}}},
		Engine:            "InnoDB",
		CharacterSet:      "utf8mb4",
	}
	ddl, fingerprint, err := r.compileTable(spec)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, want := range []string{
		"CREATE TABLE `orders`",
		"`id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT",
		"`order_no` VARCHAR(64) NOT NULL",
		"`amount` DECIMAL(18,2) NOT NULL",
		"`created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP",
		"PRIMARY KEY (`id`)",
		"UNIQUE KEY `uk_orders_order_no` (`order_no`)",
		"KEY `idx_orders_created_at` (`created_at` DESC)",
		"ENGINE=InnoDB",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL missing %q:\n%s", want, ddl)
		}
	}
	if fingerprint == "" {
		t.Error("empty fingerprint")
	}
}

func TestCompileTableRejectsUnknownColumn(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowCreateSchemas: true}
	spec := TableCreateSpec{
		Name:       "orders",
		Columns:    []TableColumnSpec{{Name: "id", Type: ColumnTypeSpec{Kind: "BIGINT"}}},
		PrimaryKey: []string{"ghost"},
	}
	if _, _, err := r.compileTable(spec); err == nil {
		t.Error("expected unknown primary-key column to be rejected")
	}
}

func TestCompileTableRejectsDuplicateColumn(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowCreateSchemas: true}
	spec := TableCreateSpec{
		Name:    "orders",
		Columns: []TableColumnSpec{
			{Name: "id", Type: ColumnTypeSpec{Kind: "BIGINT"}},
			{Name: "id", Type: ColumnTypeSpec{Kind: "BIGINT"}},
		},
	}
	if _, _, err := r.compileTable(spec); err == nil {
		t.Error("expected duplicate column to be rejected")
	}
}

func TestCompileTableRejectsNonInnoDB(t *testing.T) {
	r := &DatabaseRunner{protocol: "mysql", allowCreateSchemas: true}
	spec := TableCreateSpec{
		Name:    "orders",
		Columns: []TableColumnSpec{{Name: "id", Type: ColumnTypeSpec{Kind: "BIGINT"}}},
		Engine:  "MyISAM",
	}
	if _, _, err := r.compileTable(spec); err == nil {
		t.Error("expected non-InnoDB engine to be rejected")
	}
}

func TestRenderColumnDefault(t *testing.T) {
	if got, err := renderColumnDefault(&ColumnDefault{Kind: "CURRENT_TIMESTAMP"}); err != nil || got != "CURRENT_TIMESTAMP" {
		t.Errorf("CURRENT_TIMESTAMP -> %q, %v", got, err)
	}
	if got, err := renderColumnDefault(&ColumnDefault{Kind: "LITERAL", Value: "abc"}); err != nil || got != "'abc'" {
		t.Errorf("literal abc -> %q, %v", got, err)
	}
	if got, err := renderColumnDefault(&ColumnDefault{Kind: "LITERAL", Value: 0}); err != nil || got != "0" {
		t.Errorf("literal 0 -> %q, %v", got, err)
	}
	if _, err := renderColumnDefault(&ColumnDefault{Kind: "BOGUS"}); err == nil {
		t.Error("expected unknown default kind to be rejected")
	}
}
