package connector

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestDatabaseRunnerPostgreSQLIntegration(t *testing.T) {
	endpoint := os.Getenv("WARDEN_TEST_DATABASE_ENDPOINT")
	username := os.Getenv("WARDEN_TEST_DATABASE_USERNAME")
	password := os.Getenv("WARDEN_TEST_DATABASE_PASSWORD")
	if endpoint == "" || username == "" || password == "" {
		t.Skip("WARDEN_TEST_DATABASE_ENDPOINT, WARDEN_TEST_DATABASE_USERNAME and WARDEN_TEST_DATABASE_PASSWORD are required")
	}
	runner, err := NewDatabaseRunner(DatabaseConfig{Protocol: "postgresql", Endpoint: endpoint,
		Username: username, Password: password, AllowedSchemas: []string{"public"}, IntegrationID: "integration-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	resources := runner.ListResources(context.Background())
	var instances, schemas, tables int
	for _, resource := range resources {
		switch resource.ResourceType {
		case "database_instance":
			instances++
		case "database_schema":
			schemas++
		case "database_table":
			tables++
		}
	}
	if instances != 1 || schemas != 1 || tables == 0 {
		t.Fatalf("unexpected PostgreSQL discovery counts: instances=%d schemas=%d tables=%d", instances, schemas, tables)
	}
}

func TestDatabaseRunnerDiscoversScopedPostgreSQLMetadata(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT schema_name FROM information_schema.schemata")).
		WillReturnRows(sqlmock.NewRows([]string{"schema_name"}).AddRow("audit").AddRow("public"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_schema, table_name, table_type FROM information_schema.tables")).
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "table_type"}).
			AddRow("audit", "events", "BASE TABLE").AddRow("public", "orders", "BASE TABLE"))

	runner, err := NewDatabaseRunner(DatabaseConfig{Protocol: "postgresql", Endpoint: "postgresql://db.example.test:5432/orders?sslmode=require",
		Username: "warden_reader", Password: "secret-password", AllowedSchemas: []string{"public"}, IntegrationID: "integration-1", DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	resources := runner.ListResources(context.Background())
	encoded, _ := json.Marshal(resources)
	text := string(encoded)
	for _, expected := range []string{"database_instance", "schema/public", "table/public/orders", "integration-1"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("discovery result missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "audit") || strings.Contains(text, "secret-password") {
		t.Fatalf("discovery leaked an out-of-scope schema or password: %s", text)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRunnerDescribesTableWithoutReadingRows(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2")).
		WithArgs("public", "orders").WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable", "column_default", "ordinal_position"}).
		AddRow("id", "bigint", "NO", "", 1).AddRow("status", "text", "YES", "", 2))
	runner, err := NewDatabaseRunner(DatabaseConfig{Protocol: "postgresql", Endpoint: "postgresql://db.example.test/orders",
		Username: "reader", Password: "password", AllowedSchemas: []string{"public"}, IntegrationID: "integration-1", DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	task := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "database_table", ExternalID: "table/public/orders"},
		Action: domain.TaskAction{Name: "database.table.describe"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}}
	result, lease, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	columns, ok := result["columns"].([]map[string]any)
	if !ok || len(columns) != 2 || columns[0]["name"] != "id" {
		t.Fatalf("unexpected describe result: %#v", result)
	}
	if lease.Provider != "DATABASE_CREDENTIAL_REFERENCE" {
		t.Fatalf("unexpected lease: %#v", lease)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRunnerQueriesStructuredPostgreSQLRows(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2")).
		WithArgs("public", "user_credits_bill").WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable", "column_default", "ordinal_position"}).
		AddRow("uid", "bigint", "NO", "", 1).
		AddRow("amount", "numeric", "NO", "", 2).
		AddRow("cst_created", "timestamp", "NO", "", 3))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT "uid","amount","cst_created" FROM "public"."user_credits_bill" WHERE "uid" IN ($1,$2) AND "amount" < $3 ORDER BY "cst_created" DESC LIMIT 101 OFFSET 0`)).
		WithArgs(float64(16035793), float64(16016006), float64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"uid", "amount", "cst_created"}).
			AddRow(int64(16035793), "-2100.00", time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)))
	runner, err := NewDatabaseRunner(DatabaseConfig{Protocol: "postgresql", Endpoint: "postgresql://db.example.test/app",
		Username: "reader", Password: "password", AllowedSchemas: []string{"public"}, IntegrationID: "integration-1", DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "database_table", ExternalID: "table/public/user_credits_bill"},
		Action:   domain.TaskAction{Name: "database.table.rows.query"},
		Grant:    domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
		Parameters: map[string]any{
			"name": "user_credits_bill", "namespace": "public",
			"columns": []any{"uid", "amount", "cst_created"},
			"filters": []any{
				map[string]any{"column": "uid", "operator": "in", "value": []any{float64(16035793), float64(16016006)}},
				map[string]any{"column": "amount", "operator": "lt", "value": float64(0)},
			},
			"orderBy": []any{map[string]any{"column": "cst_created", "direction": "desc"}},
			"limit":   float64(100),
		},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := result["rows"].([]map[string]any)
	if !ok || len(rows) != 1 || rows[0]["uid"] != int64(16035793) || rows[0]["amount"] != "-2100.00" {
		t.Fatalf("unexpected query result: %#v", result)
	}
	if result["hasMore"] != false || result["rowCount"] != 1 {
		t.Fatalf("unexpected pagination result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRunnerQueriesStructuredMySQLRows(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.columns WHERE table_schema=? AND table_name=?")).
		WithArgs("app", "user_credits").WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable", "column_default", "ordinal_position"}).
		AddRow("uid", "bigint", "NO", "", 1).
		AddRow("balance_credits_num", "decimal", "NO", "", 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `uid`,`balance_credits_num` FROM `app`.`user_credits` WHERE `uid` = ? LIMIT 3 OFFSET 0")).
		WithArgs(float64(16035793)).WillReturnRows(sqlmock.NewRows([]string{"uid", "balance_credits_num"}).
		AddRow(int64(16035793), []byte("-2100.00")).
		AddRow(int64(16035793), []byte("-2200.00")).
		AddRow(int64(16035793), []byte("-2300.00")))
	runner, err := NewDatabaseRunner(DatabaseConfig{Protocol: "mysql", Endpoint: "mysql://db.example.test/app",
		Username: "reader", Password: "password", AllowedSchemas: []string{"app"}, IntegrationID: "integration-1", DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "database_table", ExternalID: "table/app/user_credits"},
		Action:   domain.TaskAction{Name: "database.table.rows.query"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
		Parameters: map[string]any{
			"name": "user_credits", "namespace": "app", "columns": []any{"uid", "balance_credits_num"},
			"filters": []any{map[string]any{"column": "uid", "operator": "eq", "value": float64(16035793)}},
			"limit":   float64(2),
		},
	}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	rows := result["rows"].([]map[string]any)
	if len(rows) != 2 || result["hasMore"] != true || rows[0]["balance_credits_num"] != "-2100.00" {
		t.Fatalf("unexpected bounded MySQL result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRunnerRejectsRawSQLAndUnknownColumns(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing()
	runner, err := NewDatabaseRunner(DatabaseConfig{Protocol: "postgresql", Endpoint: "postgresql://db.example.test/app",
		Username: "reader", Password: "password", AllowedSchemas: []string{"public"}, IntegrationID: "integration-1", DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	task := domain.TaskEnvelope{
		Resource: domain.TaskResource{Type: "database_table", ExternalID: "table/public/user_credits_bill"},
		Action:   domain.TaskAction{Name: "database.table.rows.query"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
		Parameters: map[string]any{
			"name": "user_credits_bill", "namespace": "public", "columns": []any{"uid"},
			"sql": "SELECT * FROM secrets",
		},
	}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "unsupported database query parameter") {
		t.Fatalf("raw SQL parameter was not rejected: %v", err)
	}
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2")).
		WithArgs("public", "user_credits_bill").WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "is_nullable", "column_default", "ordinal_position"}).
		AddRow("uid", "bigint", "NO", "", 1))
	delete(task.Parameters, "sql")
	task.Parameters["columns"] = []any{"uid", "password FROM credentials"}
	if _, _, err := runner.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unknown/injected column was not rejected: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRunnerGetsSelectedSchema(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_schema, table_name, table_type FROM information_schema.tables")).
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "table_type"}).
			AddRow("audit", "events", "BASE TABLE").AddRow("public", "orders", "BASE TABLE"))
	runner, err := NewDatabaseRunner(DatabaseConfig{Protocol: "postgresql", Endpoint: "postgresql://db.example.test/orders",
		Username: "reader", Password: "password", AllowAll: true, IntegrationID: "integration-1", DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	task := domain.TaskEnvelope{Resource: domain.TaskResource{Type: "database_schema", ExternalID: "schema/public"},
		Action: domain.TaskAction{Name: "database.schema.get"}, Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)}}
	result, _, err := runner.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	tables, ok := result["tables"].([]databaseTable)
	if !ok || len(tables) != 1 || tables[0].Schema != "public" || tables[0].Name != "orders" {
		t.Fatalf("unexpected schema result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRunnerValidatesEndpoints(t *testing.T) {
	tests := []DatabaseConfig{
		{Protocol: "postgresql", Endpoint: "mysql://db.example.test/orders", Username: "reader", Password: "password"},
		{Protocol: "mysql", Endpoint: "mysql://user:password@db.example.test/orders", Username: "reader", Password: "password"},
		{Protocol: "redis", Endpoint: "redis://db.example.test/cache", Username: "reader", Password: "password"},
	}
	for _, config := range tests {
		if runner, err := NewDatabaseRunner(config); err == nil {
			runner.Close()
			t.Fatalf("invalid database config was accepted: %#v", config)
		}
	}
}
