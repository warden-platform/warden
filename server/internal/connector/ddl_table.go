package connector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

const defaultTableEngine = "InnoDB"

// compileTable validates a create-table request and returns the parameterized
// DDL plus its structural fingerprint. It performs no writes.
func (r *DatabaseRunner) compileTable(spec TableCreateSpec) (string, string, error) {
	if !validMySQLName(spec.Name) {
		return "", "", fmt.Errorf("table name %q is not a valid MySQL identifier", spec.Name)
	}
	if len(spec.Columns) < 1 {
		return "", "", fmt.Errorf("at least one column is required")
	}
	columnNames := make(map[string]bool, len(spec.Columns))
	fingerprint := tableFingerprint{engine: defaultTableEngine, charset: defaultSchemaCharset}
	if spec.Engine != "" {
		if strings.ToLower(spec.Engine) != "innodb" {
			return "", "", fmt.Errorf("only InnoDB is supported")
		}
		fingerprint.engine = spec.Engine
	}
	if spec.CharacterSet != "" {
		fingerprint.charset = strings.ToLower(spec.CharacterSet)
	}
	if err := r.checkCharsetCollation(fingerprint.charset, spec.Collation); err != nil {
		return "", "", err
	}
	fingerprint.collation = strings.ToLower(spec.Collation)

	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(quoteMySQLIdentifier(spec.Name))
	b.WriteString(" (\n")
	lines := make([]string, 0, len(spec.Columns)+1+len(spec.UniqueConstraints)+len(spec.Indexes))
	for _, column := range spec.Columns {
		if !validMySQLName(column.Name) {
			return "", "", fmt.Errorf("column name %q is not a valid MySQL identifier", column.Name)
		}
		if columnNames[column.Name] {
			return "", "", fmt.Errorf("duplicate column %q", column.Name)
		}
		columnNames[column.Name] = true
		if err := validateColumnType(column.Type); err != nil {
			return "", "", fmt.Errorf("column %q: %w", column.Name, err)
		}
		if column.AutoIncrement && column.Nullable {
			return "", "", fmt.Errorf("auto-increment column %q must not be nullable", column.Name)
		}
		if column.Default != nil && column.AutoIncrement {
			return "", "", fmt.Errorf("auto-increment column %q must not have a default", column.Name)
		}
		if column.Default != nil {
			if _, err := renderColumnDefault(column.Default); err != nil {
				return "", "", fmt.Errorf("column %q: %w", column.Name, err)
			}
		}
		line := "  " + quoteMySQLIdentifier(column.Name) + " " + renderColumnType(column.Type)
		if column.Nullable {
			line += " NULL"
		} else {
			line += " NOT NULL"
		}
		if column.Default != nil {
			def, _ := renderColumnDefault(column.Default)
			line += " DEFAULT " + def
		}
		if column.AutoIncrement {
			line += " AUTO_INCREMENT"
		}
		lines = append(lines, line)
		fingerprint.columns = append(fingerprint.columns, columnSig{
			name: column.Name, typ: renderColumnType(column.Type),
			nullable: column.Nullable, autoIncrement: column.AutoIncrement,
		})
	}
	if len(spec.PrimaryKey) > 0 {
		if err := requireKnownColumns(spec.PrimaryKey, columnNames); err != nil {
			return "", "", fmt.Errorf("primaryKey: %w", err)
		}
		lines = append(lines, "  PRIMARY KEY ("+quoteColumnList(spec.PrimaryKey)+")")
	}
	for _, unique := range spec.UniqueConstraints {
		if !validMySQLName(unique.Name) {
			return "", "", fmt.Errorf("unique constraint name %q is not a valid identifier", unique.Name)
		}
		if err := requireKnownColumns(unique.Columns, columnNames); err != nil {
			return "", "", fmt.Errorf("unique constraint %q: %w", unique.Name, err)
		}
		lines = append(lines, "  UNIQUE KEY "+quoteMySQLIdentifier(unique.Name)+" ("+quoteColumnList(unique.Columns)+")")
		fingerprint.indexes = append(fingerprint.indexes, indexSig{name: unique.Name, unique: true, columns: unique.Columns})
	}
	for _, index := range spec.Indexes {
		if !validMySQLName(index.Name) {
			return "", "", fmt.Errorf("index name %q is not a valid identifier", index.Name)
		}
		cols := make([]string, 0, len(index.Columns))
		dirs := make([]string, 0, len(index.Columns))
		for _, ic := range index.Columns {
			if !columnNames[ic.Name] {
				return "", "", fmt.Errorf("index %q references unknown column %q", index.Name, ic.Name)
			}
			dir := strings.ToUpper(ic.Direction)
			if dir == "" {
				dir = "ASC"
			}
			if dir != "ASC" && dir != "DESC" {
				return "", "", fmt.Errorf("index %q column %q has invalid direction", index.Name, ic.Name)
			}
			cols = append(cols, ic.Name)
			dirs = append(dirs, dir)
		}
		lines = append(lines, "  KEY "+quoteMySQLIdentifier(index.Name)+" ("+quoteColumnListWithDirs(cols, dirs)+")")
		fingerprint.indexes = append(fingerprint.indexes, indexSig{name: index.Name, columns: cols})
	}
	b.WriteString(strings.Join(lines, ",\n"))
	b.WriteString("\n) ENGINE=InnoDB DEFAULT CHARSET=" + fingerprint.charset)
	if spec.Collation != "" {
		b.WriteString(" COLLATE=" + spec.Collation)
	}
	return b.String(), fingerprintString(fingerprint.canonical()), nil
}

func renderColumnDefault(def *ColumnDefault) (string, error) {
	switch def.Kind {
	case "CURRENT_TIMESTAMP":
		return "CURRENT_TIMESTAMP", nil
	case "LITERAL":
		if !queryScalar(def.Value) {
			return "", fmt.Errorf("literal default must be a string, number or boolean")
		}
		switch value := def.Value.(type) {
		case string:
			return "'" + escapeMySQLString(value) + "'", nil
		case bool:
			if value {
				return "TRUE", nil
			}
			return "FALSE", nil
		default:
			return renderMySQLLiteral(def.Value), nil
		}
	default:
		return "", fmt.Errorf("unsupported default kind %q", def.Kind)
	}
}

func requireKnownColumns(columns []string, known map[string]bool) error {
	if len(columns) < 1 {
		return fmt.Errorf("must reference at least one column")
	}
	for _, column := range columns {
		if !known[column] {
			return fmt.Errorf("references unknown column %q", column)
		}
	}
	return nil
}

func quoteColumnList(columns []string) string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, quoteMySQLIdentifier(column))
	}
	return strings.Join(quoted, ",")
}

func quoteColumnListWithDirs(columns, dirs []string) string {
	quoted := make([]string, 0, len(columns))
	for i := range columns {
		quoted = append(quoted, quoteMySQLIdentifier(columns[i])+" "+dirs[i])
	}
	return strings.Join(quoted, ",")
}

// tablePreview is the dry-run for create-table.
func (r *DatabaseRunner) tablePreview(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.leaseForDDL(task.Parameters)
	schema, err := databaseSchemaID(task.Resource.ExternalID)
	if err != nil || !r.schemaAllowed(schema) {
		return nil, lease, fmt.Errorf("database schema is outside the integration scope")
	}
	var spec TableCreateSpec
	if err := decodeDDLSpec(task.Parameters, &spec); err != nil {
		return nil, lease, err
	}
	ddl, fingerprint, err := r.compileTable(spec)
	if err != nil {
		return nil, lease, err
	}
	exists, err := r.tableExists(ctx, schema, spec.Name)
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{
		"schema":     schema,
		"name":       spec.Name,
		"ddl":        ddl,
		"exists":     exists,
		"previewHash": fingerprint,
	}, lease, nil
}

// createTable executes the create-table DDL with idempotency (§14.2).
func (r *DatabaseRunner) createTable(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.leaseForDDL(task.Parameters)
	schema, err := databaseSchemaID(task.Resource.ExternalID)
	if err != nil || !r.schemaAllowed(schema) {
		return nil, lease, fmt.Errorf("database schema is outside the integration scope")
	}
	var spec TableCreateSpec
	if err := decodeDDLSpec(task.Parameters, &spec); err != nil {
		return nil, lease, err
	}
	if err := requirePreviewHash(task.Parameters, "previewHash"); err != nil {
		return nil, lease, err
	}
	ddl, fingerprint, err := r.compileTable(spec)
	if err != nil {
		return nil, lease, err
	}
	if task.Parameters["previewHash"] != fingerprint {
		return nil, lease, fmt.Errorf("PREVIEW_CONFLICT: table spec changed after preview")
	}
	exists, err := r.tableExists(ctx, schema, spec.Name)
	if err != nil {
		return nil, lease, err
	}
	if exists {
		live, err := r.liveTableFingerprint(ctx, schema, spec.Name)
		if err != nil {
			return nil, lease, err
		}
		if live == fingerprint {
			return map[string]any{"schema": schema, "name": spec.Name, "idempotentReplay": true, "ddl": ddl}, lease, nil
		}
		return nil, lease, fmt.Errorf("TABLE_DEFINITION_CONFLICT: table %s.%s exists with a different definition", schema, spec.Name)
	}
	if _, err := r.poolFor("database.table.create").ExecContext(ctx, ddl); err != nil {
		return nil, lease, err
	}
	return map[string]any{"schema": schema, "name": spec.Name, "ddl": ddl, "created": true}, lease, nil
}

func (r *DatabaseRunner) tableExists(ctx context.Context, schema, table string) (bool, error) {
	query := `SELECT 1 FROM information_schema.tables WHERE table_schema=? AND table_name=?`
	var one int
	err := r.poolFor("database.table.create").QueryRowContext(ctx, query, schema, table).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// liveTableFingerprint reconstructs the live definition into the same canonical
// shape as compileTable so idempotency can compare the two. Column defaults are
// intentionally excluded for the first release (normalizing server-side default
// rendering is fragile); a default-only drift therefore still replays idempotently.
func (r *DatabaseRunner) liveTableFingerprint(ctx context.Context, schema, table string) (string, error) {
	query := `SELECT column_name, column_type, is_nullable, COALESCE(extra,'')
		FROM information_schema.columns WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`
	rows, err := r.poolFor("database.table.create").QueryContext(ctx, query, schema, table)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	fingerprint := tableFingerprint{}
	for rows.Next() {
		var name, columnType, nullable, extra string
		if err := rows.Scan(&name, &columnType, &nullable, &extra); err != nil {
			return "", err
		}
		fingerprint.columns = append(fingerprint.columns, columnSig{
			name: name, typ: columnType,
			nullable:      nullable == "YES",
			autoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment"),
		})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	indexes, err := r.describeTableIndexes(ctx, schema, table)
	if err != nil {
		return "", err
	}
	for _, index := range indexes {
		name, _ := index["name"].(string)
		unique, _ := index["unique"].(bool)
		cols := make([]string, 0)
		for _, column := range stringSlice(index["columns"]) {
			cols = append(cols, column)
		}
		fingerprint.indexes = append(fingerprint.indexes, indexSig{name: name, unique: unique, columns: cols})
	}
	info, err := r.describeTableInfo(ctx, schema, table)
	if err != nil {
		return "", err
	}
	fingerprint.engine, _ = info["engine"].(string)
	fingerprint.collation, _ = info["collation"].(string)
	fingerprint.charset, _ = info["charset"].(string)
	return fingerprintString(fingerprint.canonical()), nil
}

type tableFingerprint struct {
	columns   []columnSig
	indexes   []indexSig
	engine    string
	charset   string
	collation string
}

type columnSig struct {
	name          string
	typ           string
	nullable      bool
	autoIncrement bool
}

type indexSig struct {
	name    string
	unique  bool
	columns []string
}

func (f tableFingerprint) canonical() string {
	var b strings.Builder
	for _, column := range f.columns {
		b.WriteString("c:")
		b.WriteString(column.name)
		b.WriteString("=")
		b.WriteString(normalizeColumnType(column.typ))
		b.WriteString(":")
		b.WriteString(boolToken(column.nullable))
		b.WriteString(":")
		b.WriteString(boolToken(column.autoIncrement))
		b.WriteString("\n")
	}
	sort.Slice(f.indexes, func(i, j int) bool { return f.indexes[i].name < f.indexes[j].name })
	for _, index := range f.indexes {
		b.WriteString("i:")
		b.WriteString(index.name)
		b.WriteString("=")
		b.WriteString(boolToken(index.unique))
		b.WriteString(":")
		b.WriteString(strings.Join(index.columns, ","))
		b.WriteString("\n")
	}
	b.WriteString("e:")
	b.WriteString(strings.ToLower(f.engine))
	b.WriteString("\ncs:")
	b.WriteString(strings.ToLower(f.charset))
	b.WriteString("\nco:")
	b.WriteString(strings.ToLower(f.collation))
	b.WriteString("\n")
	return b.String()
}

func boolToken(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func stringSlice(value any) []string {
	switch items := value.(type) {
	case []string:
		return items
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func isNoRows(err error) bool {
	return err == sql.ErrNoRows
}

func fingerprintString(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func requirePreviewHash(parameters map[string]any, key string) error {
	_, err := requiredString(parameters, key)
	if err != nil {
		return fmt.Errorf("a preview is required before this change")
	}
	return nil
}
