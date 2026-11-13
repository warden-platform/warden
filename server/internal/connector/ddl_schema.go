package connector

import (
	"context"
	"fmt"
	"strings"
)

const defaultSchemaCharset = "utf8mb4"

var reservedSchemaNames = map[string]bool{
	"information_schema": true, "mysql": true, "performance_schema": true, "sys": true,
}

// compileSchema validates a create-database request and returns the parameterized
// DDL plus its fingerprint (name + charset + collation). It performs no writes.
func (r *DatabaseRunner) compileSchema(spec SchemaCreateSpec) (string, string, error) {
	if !validMySQLName(spec.Name) {
		return "", "", fmt.Errorf("database name %q is not a valid MySQL identifier", spec.Name)
	}
	if reservedSchemaNames[strings.ToLower(spec.Name)] {
		return "", "", fmt.Errorf("database name %q is reserved", spec.Name)
	}
	if err := r.checkCreateSchemaName(spec.Name); err != nil {
		return "", "", err
	}
	charset := spec.CharacterSet
	if charset == "" {
		charset = defaultSchemaCharset
	}
	charset = strings.ToLower(charset)
	if err := r.checkCharsetCollation(charset, spec.Collation); err != nil {
		return "", "", err
	}
	var b strings.Builder
	b.WriteString("CREATE DATABASE ")
	b.WriteString(quoteMySQLIdentifier(spec.Name))
	b.WriteString(" CHARACTER SET ")
	b.WriteString(charset)
	if spec.Collation != "" {
		b.WriteString(" COLLATE ")
		b.WriteString(spec.Collation)
	}
	fingerprint := fingerprintString(spec.Name + "\x00" + charset + "\x00" + strings.ToLower(spec.Collation))
	return b.String(), fingerprint, nil
}

// schemaPreview is the dry-run for create-database: it compiles the DDL and
// reports existence without executing it.
func (r *DatabaseRunner) schemaPreview(ctx context.Context, parameters map[string]any) (map[string]any, Lease, error) {
	lease := r.leaseForDDL(parameters)
	var spec SchemaCreateSpec
	if err := decodeDDLSpec(parameters, &spec); err != nil {
		return nil, lease, err
	}
	ddl, fingerprint, err := r.compileSchema(spec)
	if err != nil {
		return nil, lease, err
	}
	exists, err := r.schemaExists(ctx, spec.Name)
	if err != nil {
		return nil, lease, err
	}
	return map[string]any{
		"name":        spec.Name,
		"characterSet": spec.CharacterSet,
		"collation":   spec.Collation,
		"ddl":         ddl,
		"exists":      exists,
		"previewHash": fingerprint,
	}, lease, nil
}

// createSchema executes the create-database DDL with idempotency (§14.1):
// missing → create, present with equal charset/collation → idempotent success,
// present but different → DATABASE_DEFINITION_CONFLICT.
func (r *DatabaseRunner) createSchema(ctx context.Context, parameters map[string]any) (map[string]any, Lease, error) {
	lease := r.leaseForDDL(parameters)
	var spec SchemaCreateSpec
	if err := decodeDDLSpec(parameters, &spec); err != nil {
		return nil, lease, err
	}
	if err := requirePreviewHash(parameters, "previewHash"); err != nil {
		return nil, lease, err
	}
	ddl, fingerprint, err := r.compileSchema(spec)
	if err != nil {
		return nil, lease, err
	}
	if parameters["previewHash"] != fingerprint {
		return nil, lease, fmt.Errorf("PREVIEW_CONFLICT: schema spec changed after preview")
	}
	currentCharset, currentCollation, exists, err := r.schemaCurrent(ctx, spec.Name)
	if err != nil {
		return nil, lease, err
	}
	if exists {
		charset := spec.CharacterSet
		if charset == "" {
			charset = defaultSchemaCharset
		}
		if strings.ToLower(charset) == strings.ToLower(currentCharset) &&
			(spec.Collation == "" || strings.ToLower(spec.Collation) == strings.ToLower(currentCollation)) {
			return map[string]any{"name": spec.Name, "idempotentReplay": true, "ddl": ddl}, lease, nil
		}
		return nil, lease, fmt.Errorf("DATABASE_DEFINITION_CONFLICT: database %q exists with a different definition", spec.Name)
	}
	if _, err := r.poolFor("database.schema.create").ExecContext(ctx, ddl); err != nil {
		return nil, lease, err
	}
	return map[string]any{"name": spec.Name, "ddl": ddl, "created": true}, lease, nil
}

func (r *DatabaseRunner) schemaExists(ctx context.Context, name string) (bool, error) {
	_, _, exists, err := r.schemaCurrent(ctx, name)
	return exists, err
}

func (r *DatabaseRunner) schemaCurrent(ctx context.Context, name string) (string, string, bool, error) {
	query := `SELECT COALESCE(DEFAULT_CHARACTER_SET_NAME,''), COALESCE(DEFAULT_COLLATION_NAME,'')
		FROM information_schema.schemata WHERE schema_name=?`
	var charset, collation string
	err := r.poolFor("database.schema.create").QueryRowContext(ctx, query, name).Scan(&charset, &collation)
	if err != nil {
		if isNoRows(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return charset, collation, true, nil
}

// checkCreateSchemaName enforces the integration-scope name prefix. When no
// prefix is configured the name must still satisfy the identifier rules.
func (r *DatabaseRunner) checkCreateSchemaName(name string) error {
	if !r.allowCreateSchemas {
		return fmt.Errorf("creating databases is disabled for this integration")
	}
	if len(r.createSchemaNamePrefixes) == 0 {
		return nil
	}
	for _, prefix := range r.createSchemaNamePrefixes {
		if strings.HasPrefix(name, prefix) {
			return nil
		}
	}
	return fmt.Errorf("database name must start with one of the configured prefixes")
}

func (r *DatabaseRunner) checkCharsetCollation(charset, collation string) error {
	if len(r.allowedCharsets) > 0 {
		if _, ok := r.allowedCharsets[charset]; !ok {
			return fmt.Errorf("character set %q is not allowed", charset)
		}
	}
	if collation != "" && len(r.allowedCollations) > 0 {
		if _, ok := r.allowedCollations[strings.ToLower(collation)]; !ok {
			return fmt.Errorf("collation %q is not allowed", collation)
		}
	}
	return nil
}

func (r *DatabaseRunner) leaseForDDL(parameters map[string]any) Lease {
	// DDL actions still produce a credential lease on the same instance; the
	// concrete provider/pool is chosen at execution time.
	return Lease{Provider: "DATABASE_CREDENTIAL_REFERENCE", Fingerprint: r.protocol + ":" + r.host}
}
