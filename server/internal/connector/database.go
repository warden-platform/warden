package connector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

var DatabaseResourceTypes = []string{"database_instance", "database_schema", "database_table"}

var DatabaseCapabilities = []string{
	"database.instance.get@v1", "database.schema.list@v1", "database.schema.get@v1",
	"database.table.list@v1", "database.table.describe@v1", "database.table.rows.query@v1",
	"database.query.preview@v1", "database.query.execute@v1", "database.query_template.execute@v1",
	"database.schema.create.preview@v1", "database.schema.create@v1",
	"database.table.create.preview@v1", "database.table.create@v1",
}

const (
	maxDatabaseQueryColumns = 20
	maxDatabaseQueryFilters = 20
	maxDatabaseQueryRows    = 100
	maxDatabaseCellBytes    = 64 << 10
	maxDatabaseResultBytes  = 1 << 20
)

// DatabaseCredentialProfile is a purpose-bound credential for a database
// integration (e.g. a SELECT-only "query" account or a "ddl" account). The
// Agent cannot choose a profile; routing is by action type.
type DatabaseCredentialProfile struct {
	Username string
	Password string
}

type DatabaseConfig struct {
	Protocol           string
	Endpoint           string
	Username           string
	Password           string
	AllowedSchemas     []string
	AllowAll           bool
	IntegrationID      string
	DB                 *sql.DB
	CredentialProfiles map[string]DatabaseCredentialProfile
	// Create-schema governance (§7). All optional; when unset the runner
	// refuses to create databases.
	AllowCreateSchemas       bool
	CreateSchemaNamePrefixes []string
	AllowedCharsets          []string
	AllowedCollations        []string
}

type DatabaseRunner struct {
	protocol       string
	host           string
	database       string
	integrationID  string
	allowedSchemas map[string]struct{}
	allowAll       bool
	db             *sql.DB
	profileDBs     map[string]*sql.DB

	allowCreateSchemas       bool
	createSchemaNamePrefixes []string
	allowedCharsets          map[string]struct{}
	allowedCollations        map[string]struct{}
}

type databaseTable struct {
	Schema string
	Name   string
	Kind   string
}

func NewDatabaseRunner(config DatabaseConfig) (*DatabaseRunner, error) {
	protocol := strings.ToLower(strings.TrimSpace(config.Protocol))
	endpoint, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("database endpoint must be an absolute URL without credentials or fragment")
	}
	if err := rejectMetadataEndpoint(endpoint); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil, fmt.Errorf("database username is required")
	}
	if config.Password == "" {
		return nil, fmt.Errorf("database password is required")
	}
	database := strings.Trim(path.Clean(endpoint.Path), "/")
	if database == "." {
		database = ""
	}

	var driverName, dsn string
	switch protocol {
	case "postgresql":
		if endpoint.Scheme != "postgresql" && endpoint.Scheme != "postgres" {
			return nil, fmt.Errorf("PostgreSQL endpoint must use postgresql://")
		}
		if database == "" {
			database = "postgres"
			endpoint.Path = "/postgres"
		}
		endpoint.Scheme = "postgresql"
		endpoint.User = url.UserPassword(config.Username, config.Password)
		query := endpoint.Query()
		if query.Get("connect_timeout") == "" {
			query.Set("connect_timeout", "10")
		}
		endpoint.RawQuery = query.Encode()
		driverName, dsn = "pgx", endpoint.String()
	case "mysql":
		if endpoint.Scheme != "mysql" {
			return nil, fmt.Errorf("MySQL endpoint must use mysql://")
		}
		if endpoint.RawQuery != "" {
			for key := range endpoint.Query() {
				if key != "tls" {
					return nil, fmt.Errorf("unsupported MySQL endpoint query parameter %q", key)
				}
			}
		}
		driverName, dsn = "mysql", mysqlDSN(endpoint, database, config.Username, config.Password)
	default:
		return nil, fmt.Errorf("unsupported database protocol %q", protocol)
	}

	db := config.DB
	if db == nil {
		db, err = sql.Open(driverName, dsn)
		if err != nil {
			return nil, err
		}
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(time.Minute)
	db.SetConnMaxLifetime(10 * time.Minute)
	allowed := make(map[string]struct{}, len(config.AllowedSchemas))
	for _, schema := range config.AllowedSchemas {
		schema = strings.TrimSpace(schema)
		if schema == "" {
			db.Close()
			return nil, fmt.Errorf("database schema scope must not contain empty values")
		}
		allowed[schema] = struct{}{}
	}
	// Build purpose-bound pools for MySQL. sql.Open is lazy (no connection is
	// established until first use), so configured-but-unused profiles cost
	// nothing. Profile pools are MySQL-only for the first release.
	profileDBs := make(map[string]*sql.DB, len(config.CredentialProfiles))
	if protocol == "mysql" {
		for name, profile := range config.CredentialProfiles {
			username := profile.Username
			if username == "" {
				username = config.Username
			}
			profileDB, err := sql.Open("mysql", mysqlDSN(endpoint, database, username, profile.Password))
			if err != nil {
				db.Close()
				return nil, err
			}
			profileDB.SetMaxOpenConns(4)
			profileDB.SetMaxIdleConns(1)
			profileDB.SetConnMaxIdleTime(time.Minute)
			profileDB.SetConnMaxLifetime(10 * time.Minute)
			profileDBs[name] = profileDB
		}
	}
	charsets := make(map[string]struct{}, len(config.AllowedCharsets))
	for _, charset := range config.AllowedCharsets {
		charsets[strings.ToLower(strings.TrimSpace(charset))] = struct{}{}
	}
	collations := make(map[string]struct{}, len(config.AllowedCollations))
	for _, collation := range config.AllowedCollations {
		collations[strings.ToLower(strings.TrimSpace(collation))] = struct{}{}
	}
	return &DatabaseRunner{protocol: protocol, host: endpoint.Host, database: database,
		integrationID: config.IntegrationID, allowedSchemas: allowed, allowAll: config.AllowAll, db: db, profileDBs: profileDBs,
		allowCreateSchemas: config.AllowCreateSchemas, createSchemaNamePrefixes: config.CreateSchemaNamePrefixes,
		allowedCharsets: charsets, allowedCollations: collations}, nil
}

func (r *DatabaseRunner) Close() error {
	var firstErr error
	if err := r.db.Close(); err != nil {
		firstErr = err
	}
	for _, db := range r.profileDBs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// poolFor returns the connection pool for an action. DDL actions use their
// dedicated profile pool; every other action uses the discovery/query pool.
func (r *DatabaseRunner) poolFor(action string) *sql.DB {
	switch action {
	case "database.schema.create", "database.table.create":
		if ddl, ok := r.profileDBs["ddl"]; ok {
			return ddl
		}
	case "database.query.preview", "database.query.execute":
		if query, ok := r.profileDBs["query"]; ok {
			return query
		}
	}
	return r.db
}

// mysqlDSN builds a MySQL DSN from a parsed endpoint and an explicit account.
// The endpoint must already have been validated as a mysql:// URL.
func mysqlDSN(endpoint *url.URL, database, username, password string) string {
	address := endpoint.Host
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(endpoint.Hostname(), "3306")
	}
	cfg := mysql.NewConfig()
	cfg.User = username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = address
	cfg.DBName = database
	cfg.ParseTime = true
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 30 * time.Second
	if endpoint.Query().Get("tls") == "true" {
		cfg.TLSConfig = "true"
	}
	return cfg.FormatDSN()
}

func (r *DatabaseRunner) ListResources(ctx context.Context) []DiscoveredResource {
	resources, _ := r.discover(ctx)
	return resources
}

// ListResourcesComplete reports whether discovery completed in full. A partial
// result (instance present but schema/table enumeration failed) must not be
// mistaken for "everything else was deleted", so the runtime uses it to
// suppress orphaning for this integration.
func (r *DatabaseRunner) ListResourcesComplete(ctx context.Context) ([]DiscoveredResource, bool) {
	return r.discover(ctx)
}

func (r *DatabaseRunner) discover(ctx context.Context) ([]DiscoveredResource, bool) {
	complete := true
	if err := r.ping(ctx); err != nil {
		fmt.Printf("database integration %s discovery failed: %v\n", r.integrationID, err)
		return nil, false
	}
	instanceExternalID := "instance/" + r.protocol + "/" + url.PathEscape(r.host)
	instanceName := r.host
	if r.database != "" {
		instanceExternalID += "/" + url.PathEscape(r.database)
		instanceName = r.database + " @ " + r.host
	}
	resources := []DiscoveredResource{{
		ExternalID: instanceExternalID,
		Name:       instanceName, ResourceType: "database_instance", IntegrationID: r.integrationID,
		Labels: map[string]any{"protocol": r.protocol}, Annotations: map[string]any{},
		Metadata: map[string]any{"protocol": r.protocol, "host": r.host, "database": r.database},
	}}
	schemas, err := r.listSchemas(ctx)
	if err != nil {
		fmt.Printf("database integration %s schema discovery failed: %v\n", r.integrationID, err)
		complete = false
	} else {
		for _, schema := range schemas {
			resources = append(resources, DiscoveredResource{
				ExternalID: "schema/" + url.PathEscape(schema), Name: schema, Namespace: r.database,
				ResourceType: "database_schema", IntegrationID: r.integrationID,
				Labels: map[string]any{"protocol": r.protocol}, Annotations: map[string]any{},
				Metadata: map[string]any{"protocol": r.protocol, "database": r.database, "schema": schema},
			})
		}
	}
	tables, err := r.listTables(ctx)
	if err != nil {
		fmt.Printf("database integration %s table discovery failed: %v\n", r.integrationID, err)
		complete = false
	} else {
		for _, table := range tables {
			resources = append(resources, DiscoveredResource{
				ExternalID: "table/" + url.PathEscape(table.Schema) + "/" + url.PathEscape(table.Name), Name: table.Name,
				Namespace: table.Schema, ResourceType: "database_table", IntegrationID: r.integrationID,
				Labels: map[string]any{"protocol": r.protocol, "kind": table.Kind}, Annotations: map[string]any{},
				Metadata: map[string]any{"protocol": r.protocol, "database": r.database, "schema": table.Schema, "table": table.Name, "kind": table.Kind},
			})
		}
	}
	return resources, complete
}

func (r *DatabaseRunner) Execute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.lease(task.Grant.ExpiresAt)
	if err := r.ping(ctx); err != nil {
		return nil, lease, err
	}
	switch task.Action.Name {
	case "database.instance.get":
		if task.Resource.Type != "database_instance" {
			return nil, lease, fmt.Errorf("database.instance.get requires database_instance")
		}
		name, version, err := r.instanceInfo(ctx)
		return map[string]any{"protocol": r.protocol, "host": r.host, "database": name, "version": version}, lease, err
	case "database.schema.list":
		if task.Resource.Type != "database_instance" {
			return nil, lease, fmt.Errorf("database.schema.list requires database_instance")
		}
		schemas, err := r.listSchemas(ctx)
		return map[string]any{"schemas": schemas}, lease, err
	case "database.table.list":
		if task.Resource.Type != "database_instance" && task.Resource.Type != "database_schema" {
			return nil, lease, fmt.Errorf("database.table.list requires database_instance or database_schema")
		}
		tables, err := r.listTables(ctx)
		return map[string]any{"tables": tables}, lease, err
	case "database.schema.get":
		if task.Resource.Type != "database_schema" {
			return nil, lease, fmt.Errorf("database.schema.get requires database_schema")
		}
		schema, err := databaseSchemaID(task.Resource.ExternalID)
		if err != nil || !r.schemaAllowed(schema) {
			return nil, lease, fmt.Errorf("database schema is outside the integration scope")
		}
		tables, err := r.listTables(ctx)
		if err != nil {
			return nil, lease, err
		}
		filtered := make([]databaseTable, 0)
		for _, table := range tables {
			if table.Schema == schema {
				filtered = append(filtered, table)
			}
		}
		return map[string]any{"protocol": r.protocol, "database": r.database, "schema": schema, "tables": filtered}, lease, nil
	case "database.table.describe":
		if task.Resource.Type != "database_table" {
			return nil, lease, fmt.Errorf("database.table.describe requires database_table")
		}
		schema, table, err := databaseTableID(task.Resource.ExternalID)
		if err != nil || !r.schemaAllowed(schema) {
			return nil, lease, fmt.Errorf("database table is outside the integration scope")
		}
		detail, err := r.describeTableDetails(ctx, schema, table)
		return detail, lease, err
	case "database.table.rows.query":
		if task.Resource.Type != "database_table" {
			return nil, lease, fmt.Errorf("database.table.rows.query requires database_table")
		}
		schema, table, err := databaseTableID(task.Resource.ExternalID)
		if err != nil || !r.schemaAllowed(schema) {
			return nil, lease, fmt.Errorf("database table is outside the integration scope")
		}
		result, err := r.queryTableRows(ctx, schema, table, task.Parameters)
		return result, lease, err
	case "database.query.preview":
		return r.queryPreview(ctx, task)
	case "database.query.execute":
		return r.queryExecute(ctx, task)
	case "database.query_template.execute":
		return r.queryTemplateExecute(ctx, task)
	case "database.schema.create.preview":
		return r.schemaPreview(ctx, task.Parameters)
	case "database.schema.create":
		if task.Resource.Type != "database_instance" {
			return nil, lease, fmt.Errorf("database.schema.create requires database_instance")
		}
		return r.createSchema(ctx, task.Parameters)
	case "database.table.create.preview":
		return r.tablePreview(ctx, task)
	case "database.table.create":
		if task.Resource.Type != "database_schema" {
			return nil, lease, fmt.Errorf("database.table.create requires database_schema")
		}
		return r.createTable(ctx, task)
	default:
		return nil, lease, fmt.Errorf("unsupported database action %q", task.Action.Name)
	}
}

type databaseQueryFilter struct {
	Column   string
	Operator string
	Value    any
}

type databaseQueryOrder struct {
	Column    string
	Direction string
}

func (r *DatabaseRunner) queryTableRows(ctx context.Context, schema, table string, parameters map[string]any) (map[string]any, error) {
	for key := range parameters {
		switch key {
		case "name", "namespace", "columns", "filters", "orderBy", "limit", "offset":
		default:
			return nil, fmt.Errorf("unsupported database query parameter %q", key)
		}
	}
	columns, err := databaseStringList(parameters["columns"], 1, maxDatabaseQueryColumns)
	if err != nil {
		return nil, fmt.Errorf("invalid columns: %w", err)
	}
	metadata, err := r.describeTable(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	allowedColumns := make(map[string]struct{}, len(metadata))
	for _, column := range metadata {
		if name, ok := column["name"].(string); ok {
			allowedColumns[name] = struct{}{}
		}
	}
	if len(allowedColumns) == 0 {
		return nil, fmt.Errorf("database table has no visible columns")
	}
	validateColumn := func(column string) error {
		if _, ok := allowedColumns[column]; !ok {
			return fmt.Errorf("column %q does not exist on the selected table", column)
		}
		return nil
	}
	seenColumns := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if err := validateColumn(column); err != nil {
			return nil, err
		}
		if _, duplicate := seenColumns[column]; duplicate {
			return nil, fmt.Errorf("column %q is duplicated", column)
		}
		seenColumns[column] = struct{}{}
	}
	filters, err := databaseFilters(parameters["filters"], validateColumn)
	if err != nil {
		return nil, err
	}
	orders, err := databaseOrders(parameters["orderBy"], validateColumn)
	if err != nil {
		return nil, err
	}
	limit, err := databaseBoundedInt(parameters["limit"], 50, 1, maxDatabaseQueryRows)
	if err != nil {
		return nil, fmt.Errorf("invalid limit: %w", err)
	}
	offset, err := databaseBoundedInt(parameters["offset"], 0, 0, 10000)
	if err != nil {
		return nil, fmt.Errorf("invalid offset: %w", err)
	}

	quotedColumns := make([]string, 0, len(columns))
	for _, column := range columns {
		quotedColumns = append(quotedColumns, r.quoteDatabaseIdentifier(column))
	}
	query := "SELECT " + strings.Join(quotedColumns, ",") + " FROM " +
		r.quoteDatabaseIdentifier(schema) + "." + r.quoteDatabaseIdentifier(table)
	args := make([]any, 0, len(filters))
	where := make([]string, 0, len(filters))
	for _, filter := range filters {
		column := r.quoteDatabaseIdentifier(filter.Column)
		switch filter.Operator {
		case "is_null":
			where = append(where, column+" IS NULL")
		case "is_not_null":
			where = append(where, column+" IS NOT NULL")
		case "in":
			values := filter.Value.([]any)
			placeholders := make([]string, 0, len(values))
			for _, value := range values {
				args = append(args, value)
				placeholders = append(placeholders, r.databasePlaceholder(len(args)))
			}
			where = append(where, column+" IN ("+strings.Join(placeholders, ",")+")")
		default:
			sqlOperator := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=", "like": "LIKE"}[filter.Operator]
			args = append(args, filter.Value)
			where = append(where, column+" "+sqlOperator+" "+r.databasePlaceholder(len(args)))
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if len(orders) > 0 {
		orderBy := make([]string, 0, len(orders))
		for _, order := range orders {
			orderBy = append(orderBy, r.quoteDatabaseIdentifier(order.Column)+" "+order.Direction)
		}
		query += " ORDER BY " + strings.Join(orderBy, ",")
	}
	// Fetch one extra row to report pagination without ever returning more than
	// the contract limit. limit/offset are validated integers, not SQL input.
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit+1, offset)
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := r.db.QueryContext(queryCtx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultRows := make([]map[string]any, 0, limit)
	resultBytes := 0
	truncatedCells := 0
	hasMore := false
	for rows.Next() {
		if len(resultRows) == limit {
			hasMore = true
			break
		}
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			value, truncated := boundedDatabaseValue(values[i])
			if truncated {
				truncatedCells++
			}
			row[column] = value
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("serialize database row: %w", err)
		}
		if resultBytes+len(encoded) > maxDatabaseResultBytes {
			hasMore = true
			break
		}
		resultBytes += len(encoded)
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"schema": schema, "table": table, "columns": columns, "rows": resultRows,
		"rowCount": len(resultRows), "limit": limit, "offset": offset,
		"hasMore": hasMore, "truncatedCells": truncatedCells,
	}, nil
}

func databaseStringList(value any, minItems, maxItems int) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		if stringsValue, stringsOK := value.([]string); stringsOK {
			items = make([]any, len(stringsValue))
			for i := range stringsValue {
				items[i] = stringsValue[i]
			}
		} else {
			return nil, fmt.Errorf("must be an array")
		}
	}
	if len(items) < minItems || len(items) > maxItems {
		return nil, fmt.Errorf("must contain between %d and %d items", minItems, maxItems)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 128 {
			return nil, fmt.Errorf("items must be non-empty strings of at most 128 bytes")
		}
		result = append(result, text)
	}
	return result, nil
}

func databaseFilters(value any, validateColumn func(string) error) ([]databaseQueryFilter, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > maxDatabaseQueryFilters {
		return nil, fmt.Errorf("filters must be an array with at most %d items", maxDatabaseQueryFilters)
	}
	result := make([]databaseQueryFilter, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("each filter must be an object")
		}
		for key := range object {
			if key != "column" && key != "operator" && key != "value" {
				return nil, fmt.Errorf("unsupported filter property %q", key)
			}
		}
		column, columnOK := object["column"].(string)
		operator, operatorOK := object["operator"].(string)
		if !columnOK || !operatorOK || validateColumn(column) != nil {
			return nil, fmt.Errorf("filter column does not exist on the selected table")
		}
		filter := databaseQueryFilter{Column: column, Operator: operator, Value: object["value"]}
		switch operator {
		case "is_null", "is_not_null":
			if _, provided := object["value"]; provided {
				return nil, fmt.Errorf("operator %s must not include value", operator)
			}
		case "in":
			values, ok := object["value"].([]any)
			if !ok || len(values) == 0 || len(values) > 100 {
				return nil, fmt.Errorf("operator in requires 1 to 100 scalar values")
			}
			for _, scalar := range values {
				if !databaseScalar(scalar) {
					return nil, fmt.Errorf("filter values must be strings, numbers or booleans")
				}
			}
			filter.Value = values
		case "eq", "ne", "gt", "gte", "lt", "lte":
			if !databaseScalar(object["value"]) {
				return nil, fmt.Errorf("filter value must be a string, number or boolean")
			}
		case "like":
			if text, ok := object["value"].(string); !ok || len(text) > 1000 {
				return nil, fmt.Errorf("like value must be a string of at most 1000 bytes")
			}
		default:
			return nil, fmt.Errorf("unsupported filter operator %q", operator)
		}
		result = append(result, filter)
	}
	return result, nil
}

func databaseOrders(value any, validateColumn func(string) error) ([]databaseQueryOrder, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) > 3 {
		return nil, fmt.Errorf("orderBy must be an array with at most 3 items")
	}
	result := make([]databaseQueryOrder, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok || len(object) != 2 {
			return nil, fmt.Errorf("each orderBy item must contain column and direction")
		}
		column, columnOK := object["column"].(string)
		direction, directionOK := object["direction"].(string)
		direction = strings.ToUpper(direction)
		if !columnOK || !directionOK || validateColumn(column) != nil || (direction != "ASC" && direction != "DESC") {
			return nil, fmt.Errorf("invalid orderBy item")
		}
		result = append(result, databaseQueryOrder{Column: column, Direction: direction})
	}
	return result, nil
}

func databaseBoundedInt(value any, defaultValue, minValue, maxValue int) (int, error) {
	if value == nil {
		return defaultValue, nil
	}
	var parsed int
	switch number := value.(type) {
	case int:
		parsed = number
	case float64:
		parsed = int(number)
		if float64(parsed) != number {
			return 0, fmt.Errorf("must be an integer")
		}
	case json.Number:
		integer, err := number.Int64()
		if err != nil {
			return 0, fmt.Errorf("must be an integer")
		}
		parsed = int(integer)
	default:
		return 0, fmt.Errorf("must be an integer")
	}
	if parsed < minValue || parsed > maxValue {
		return 0, fmt.Errorf("must be between %d and %d", minValue, maxValue)
	}
	return parsed, nil
}

func databaseScalar(value any) bool {
	switch value.(type) {
	case string, bool, float64, int, int32, int64, json.Number:
		return true
	default:
		return false
	}
}

func (r *DatabaseRunner) quoteDatabaseIdentifier(value string) string {
	if r.protocol == "mysql" {
		return "`" + strings.ReplaceAll(value, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (r *DatabaseRunner) databasePlaceholder(position int) string {
	if r.protocol == "mysql" {
		return "?"
	}
	return fmt.Sprintf("$%d", position)
}

func boundedDatabaseValue(value any) (any, bool) {
	switch typed := value.(type) {
	case []byte:
		if len(typed) > maxDatabaseCellBytes {
			return strings.ToValidUTF8(string(typed[:maxDatabaseCellBytes]), "�"), true
		}
		return strings.ToValidUTF8(string(typed), "�"), false
	case string:
		if len(typed) > maxDatabaseCellBytes {
			return strings.ToValidUTF8(typed[:maxDatabaseCellBytes], "�"), true
		}
		return typed, false
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), false
	default:
		return value, false
	}
}

func (r *DatabaseRunner) ping(ctx context.Context) error {
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return r.db.PingContext(check)
}

func (r *DatabaseRunner) instanceInfo(ctx context.Context) (string, string, error) {
	query := "SELECT current_database(), version()"
	if r.protocol == "mysql" {
		query = "SELECT COALESCE(DATABASE(), ''), VERSION()"
	}
	var name, version string
	err := r.db.QueryRowContext(ctx, query).Scan(&name, &version)
	return name, version, err
}

func (r *DatabaseRunner) listSchemas(ctx context.Context) ([]string, error) {
	query := `SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast') ORDER BY schema_name`
	if r.protocol == "mysql" {
		query = `SELECT schema_name FROM information_schema.schemata
			WHERE schema_name NOT IN ('information_schema','mysql','performance_schema','sys') ORDER BY schema_name`
	}
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			return nil, err
		}
		if r.schemaAllowed(schema) {
			result = append(result, schema)
		}
	}
	return result, rows.Err()
}

func (r *DatabaseRunner) listTables(ctx context.Context) ([]databaseTable, error) {
	query := `SELECT table_schema, table_name, table_type FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog','information_schema','pg_toast') ORDER BY table_schema, table_name`
	if r.protocol == "mysql" {
		query = `SELECT table_schema, table_name, table_type FROM information_schema.tables
			WHERE table_schema NOT IN ('information_schema','mysql','performance_schema','sys') ORDER BY table_schema, table_name`
	}
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []databaseTable
	for rows.Next() {
		var table databaseTable
		if err := rows.Scan(&table.Schema, &table.Name, &table.Kind); err != nil {
			return nil, err
		}
		if r.schemaAllowed(table.Schema) {
			result = append(result, table)
		}
	}
	return result, rows.Err()
}

func (r *DatabaseRunner) describeTable(ctx context.Context, schema, table string) ([]map[string]any, error) {
	query := `SELECT column_name, data_type, is_nullable, COALESCE(column_default,''), ordinal_position
		FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`
	args := []any{schema, table}
	if r.protocol == "mysql" {
		query = `SELECT column_name, data_type, is_nullable, COALESCE(column_default,''), ordinal_position
			FROM information_schema.columns WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []map[string]any
	for rows.Next() {
		var name, dataType, nullable, defaultValue string
		var position int
		if err := rows.Scan(&name, &dataType, &nullable, &defaultValue, &position); err != nil {
			return nil, err
		}
		columns = append(columns, map[string]any{"name": name, "dataType": dataType,
			"nullable": nullable == "YES", "default": defaultValue, "position": position})
	}
	return columns, rows.Err()
}

// describeTableDetails returns the full metadata payload for the describe
// action. Columns are always present; indexes, constraints and table-level
// attributes (engine/collation/charset) are MySQL-only for the first release
// (see the implementation plan D1), so PostgreSQL returns columns only.
func (r *DatabaseRunner) describeTableDetails(ctx context.Context, schema, table string) (map[string]any, error) {
	columns, err := r.describeTable(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	detail := map[string]any{"schema": schema, "table": table, "columns": columns}
	if r.protocol != "mysql" {
		return detail, nil
	}
	indexes, err := r.describeTableIndexes(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	constraints, err := r.describeTableConstraints(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	info, err := r.describeTableInfo(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	detail["indexes"] = indexes
	detail["constraints"] = constraints
	detail["engine"] = info["engine"]
	detail["collation"] = info["collation"]
	detail["charset"] = info["charset"]
	detail["estimatedRows"] = info["estimatedRows"]
	detail["rowCountEstimated"] = true
	if info["statsUpdatedAt"] != nil {
		detail["statsUpdatedAt"] = info["statsUpdatedAt"]
	}
	return detail, nil
}

// describeTableIndexes reports secondary/primary indexes in column order. It
// feeds DDL fingerprinting and the EXPLAIN cost gate.
func (r *DatabaseRunner) describeTableIndexes(ctx context.Context, schema, table string) ([]map[string]any, error) {
	query := `SELECT index_name, non_unique, column_name, index_type
		FROM information_schema.statistics
		WHERE table_schema=? AND table_name=? ORDER BY index_name, seq_in_index`
	rows, err := r.db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type indexAccumulator struct {
		unique  bool
		kind    string
		columns []string
	}
	order := make([]string, 0)
	byName := make(map[string]*indexAccumulator)
	for rows.Next() {
		var name, column, kind string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &column, &kind); err != nil {
			return nil, err
		}
		acc, ok := byName[name]
		if !ok {
			acc = &indexAccumulator{unique: nonUnique == 0, kind: kind}
			byName[name] = acc
			order = append(order, name)
		}
		acc.columns = append(acc.columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	indexes := make([]map[string]any, 0, len(order))
	for _, name := range order {
		acc := byName[name]
		indexes = append(indexes, map[string]any{"name": name, "unique": acc.unique,
			"type": acc.kind, "columns": acc.columns})
	}
	return indexes, nil
}

// describeTableConstraints reports table constraints (PRIMARY KEY, UNIQUE,
// FOREIGN KEY, CHECK) with their column lists and, for foreign keys, the
// referenced table/columns. It feeds DDL fingerprinting and the validator.
func (r *DatabaseRunner) describeTableConstraints(ctx context.Context, schema, table string) ([]map[string]any, error) {
	query := `SELECT tc.constraint_name, tc.constraint_type, kcu.column_name,
			kcu.referenced_table_schema, kcu.referenced_table_name, kcu.referenced_column_name
		FROM information_schema.table_constraints tc
		LEFT JOIN information_schema.key_column_usage kcu
			ON kcu.constraint_schema=tc.constraint_schema AND kcu.constraint_name=tc.constraint_name
			AND kcu.table_schema=tc.table_schema AND kcu.table_name=tc.table_name
		WHERE tc.table_schema=? AND tc.table_name=?
		ORDER BY tc.constraint_name, kcu.ordinal_position`
	rows, err := r.db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type constraintAccumulator struct {
		kind        string
		columns     []string
		refSchema   string
		refTable    string
		refColumns  []string
	}
	order := make([]string, 0)
	byName := make(map[string]*constraintAccumulator)
	for rows.Next() {
		var name, kind string
		var column, refSchema, refTable, refColumn sql.NullString
		if err := rows.Scan(&name, &kind, &column, &refSchema, &refTable, &refColumn); err != nil {
			return nil, err
		}
		acc, ok := byName[name]
		if !ok {
			acc = &constraintAccumulator{kind: kind}
			byName[name] = acc
			order = append(order, name)
		}
		if column.Valid {
			acc.columns = append(acc.columns, column.String)
		}
		if refTable.Valid {
			acc.refSchema = refSchema.String
			acc.refTable = refTable.String
		}
		if refColumn.Valid {
			acc.refColumns = append(acc.refColumns, refColumn.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	constraints := make([]map[string]any, 0, len(order))
	for _, name := range order {
		acc := byName[name]
		constraint := map[string]any{"name": name, "type": acc.kind, "columns": acc.columns}
		if acc.refTable != "" {
			constraint["referencedSchema"] = acc.refSchema
			constraint["referencedTable"] = acc.refTable
			constraint["referencedColumns"] = acc.refColumns
		}
		constraints = append(constraints, constraint)
	}
	return constraints, nil
}

// describeTableInfo reports the storage engine, collation, (derived) character
// set and row-count statistics for a MySQL table. TABLE_ROWS is an
// INFORMATION_SCHEMA estimate for InnoDB, so callers must surface it as an
// estimate rather than an exact count.
func (r *DatabaseRunner) describeTableInfo(ctx context.Context, schema, table string) (map[string]any, error) {
	var engine, collation string
	var estimatedRows, dataLength uint64
	var statsUpdatedAt sql.NullTime
	query := `SELECT COALESCE(engine,''), COALESCE(table_collation,''),
		COALESCE(table_rows,0), COALESCE(data_length,0),
		COALESCE(update_time, create_time)
		FROM information_schema.tables WHERE table_schema=? AND table_name=?`
	if err := r.db.QueryRowContext(ctx, query, schema, table).Scan(&engine, &collation, &estimatedRows, &dataLength, &statsUpdatedAt); err != nil {
		return nil, err
	}
	charset := collation
	if idx := strings.IndexByte(collation, '_'); idx > 0 {
		charset = collation[:idx]
	}
	info := map[string]any{
		"engine": engine, "collation": collation, "charset": charset,
		"estimatedRows": estimatedRows, "dataLength": dataLength,
	}
	if statsUpdatedAt.Valid {
		info["statsUpdatedAt"] = statsUpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return info, nil
}

func (r *DatabaseRunner) schemaAllowed(schema string) bool {
	if len(r.allowedSchemas) == 0 {
		return r.allowAll
	}
	_, ok := r.allowedSchemas[schema]
	return ok
}

func (r *DatabaseRunner) lease(expiresAt time.Time) Lease {
	return Lease{Provider: "DATABASE_CREDENTIAL_REFERENCE", Fingerprint: r.protocol + ":" + r.host, ExpiresAt: expiresAt}
}

func databaseTableID(externalID string) (string, string, error) {
	parts := strings.Split(externalID, "/")
	if len(parts) != 3 || parts[0] != "table" {
		return "", "", fmt.Errorf("invalid database table resource")
	}
	schema, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", err
	}
	table, err := url.PathUnescape(parts[2])
	if err != nil || schema == "" || table == "" {
		return "", "", fmt.Errorf("invalid database table resource")
	}
	return schema, table, nil
}

func databaseSchemaID(externalID string) (string, error) {
	parts := strings.Split(externalID, "/")
	if len(parts) != 2 || parts[0] != "schema" {
		return "", fmt.Errorf("invalid database schema resource")
	}
	schema, err := url.PathUnescape(parts[1])
	if err != nil || schema == "" || strings.Contains(schema, "/") {
		return "", fmt.Errorf("invalid database schema resource")
	}
	return schema, nil
}
