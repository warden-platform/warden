package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// DDL specs are the structured inputs to the create-database / create-table
// actions. They are decoded strictly (unknown fields rejected) and every
// identifier/type/option is validated against a fixed whitelist before any DDL
// string is assembled (§7, §8). Free-form DDL fragments are never accepted.

type ColumnTypeSpec struct {
	Kind      string `json:"kind"`
	Unsigned  bool   `json:"unsigned,omitempty"`
	Length    int    `json:"length,omitempty"`
	Precision int    `json:"precision,omitempty"`
	Scale     int    `json:"scale,omitempty"`
}

type ColumnDefault struct {
	Kind  string `json:"kind"`
	Value any    `json:"value,omitempty"`
}

type TableColumnSpec struct {
	Name          string         `json:"name"`
	Type          ColumnTypeSpec `json:"type"`
	Nullable      bool           `json:"nullable,omitempty"`
	AutoIncrement bool           `json:"autoIncrement,omitempty"`
	Default       *ColumnDefault `json:"default,omitempty"`
}

type UniqueConstraint struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

type IndexColumn struct {
	Name      string `json:"name"`
	Direction string `json:"direction,omitempty"`
}

type IndexSpec struct {
	Name    string        `json:"name"`
	Columns []IndexColumn `json:"columns"`
}

type TableCreateSpec struct {
	Name              string             `json:"name"`
	Columns           []TableColumnSpec  `json:"columns"`
	PrimaryKey        []string           `json:"primaryKey,omitempty"`
	UniqueConstraints []UniqueConstraint `json:"uniqueConstraints,omitempty"`
	Indexes           []IndexSpec        `json:"indexes,omitempty"`
	Engine            string             `json:"engine,omitempty"`
	CharacterSet      string             `json:"characterSet,omitempty"`
	Collation         string             `json:"collation,omitempty"`
	PreviewHash       string             `json:"previewHash,omitempty"`
	PreviewExecution  string             `json:"previewExecutionId,omitempty"`
}

type SchemaCreateSpec struct {
	Name             string `json:"name"`
	CharacterSet     string `json:"characterSet,omitempty"`
	Collation        string `json:"collation,omitempty"`
	PreviewHash      string `json:"previewHash,omitempty"`
	PreviewExecution string `json:"previewExecutionId,omitempty"`
}

var mysqlIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]{0,63}$`)

// validMySQLName rejects names that are not safe to use as an identifier even
// when backtick-quoted, matching MySQL/Warden naming rules (§7).
func validMySQLName(name string) bool {
	return mysqlIdentifierPattern.MatchString(name)
}

func decodeDDLSpec(parameters map[string]any, target any) error {
	data, err := json.Marshal(parameters)
	if err != nil {
		return fmt.Errorf("encode DDL parameters: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode DDL spec: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("decode DDL spec: unexpected trailing data")
	}
	return nil
}

// renderColumnType emits the canonical SQL type for a validated column type.
// validateColumnType must be called first.
func renderColumnType(t ColumnTypeSpec) string {
	switch t.Kind {
	case "TINYINT", "SMALLINT", "INT", "BIGINT":
		if t.Unsigned {
			return t.Kind + " UNSIGNED"
		}
		return t.Kind
	case "DECIMAL":
		if t.Scale > 0 {
			return fmt.Sprintf("DECIMAL(%d,%d)", t.Precision, t.Scale)
		}
		return fmt.Sprintf("DECIMAL(%d)", t.Precision)
	case "CHAR", "VARCHAR", "BINARY", "VARBINARY":
		return fmt.Sprintf("%s(%d)", t.Kind, t.Length)
	case "DATETIME", "TIMESTAMP":
		if t.Precision > 0 {
			return fmt.Sprintf("%s(%d)", t.Kind, t.Precision)
		}
		return t.Kind
	default:
		return t.Kind
	}
}

func validateColumnType(t ColumnTypeSpec) error {
	switch t.Kind {
	case "TINYINT", "SMALLINT", "INT", "BIGINT":
		if t.Length != 0 || t.Precision != 0 || t.Scale != 0 {
			return fmt.Errorf("type %s does not accept length/precision/scale", t.Kind)
		}
	case "DECIMAL":
		if t.Precision < 1 || t.Precision > 65 {
			return fmt.Errorf("DECIMAL precision must be between 1 and 65")
		}
		if t.Scale < 0 || t.Scale > 30 || t.Scale > t.Precision {
			return fmt.Errorf("DECIMAL scale must be between 0 and 30 and not exceed precision")
		}
		if t.Unsigned {
			return fmt.Errorf("DECIMAL does not accept unsigned")
		}
	case "CHAR":
		if t.Length < 1 || t.Length > 255 {
			return fmt.Errorf("CHAR length must be between 1 and 255")
		}
	case "VARCHAR":
		if t.Length < 1 || t.Length > 16383 {
			return fmt.Errorf("VARCHAR length must be between 1 and 16383")
		}
	case "BINARY", "VARBINARY":
		if t.Length < 1 || t.Length > 16383 {
			return fmt.Errorf("%s length must be between 1 and 16383", t.Kind)
		}
	case "TEXT", "DATE", "BOOLEAN", "JSON":
		if t.Length != 0 || t.Precision != 0 || t.Scale != 0 || t.Unsigned {
			return fmt.Errorf("type %s does not accept modifiers", t.Kind)
		}
	case "DATETIME", "TIMESTAMP":
		if t.Precision < 0 || t.Precision > 6 {
			return fmt.Errorf("%s precision must be between 0 and 6", t.Kind)
		}
		if t.Unsigned || t.Length != 0 || t.Scale != 0 {
			return fmt.Errorf("type %s does not accept these modifiers", t.Kind)
		}
	default:
		return fmt.Errorf("unsupported column type %q", t.Kind)
	}
	return nil
}

// normalizeColumnType folds a stored information_schema COLUMN_TYPE into the
// same canonical form emitColumnType produces, so target and live definitions
// can be compared for fingerprinting.
func normalizeColumnType(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	// BOOLEAN is stored by MySQL as tinyint(1).
	if value == "BOOLEAN" {
		return "TINYINT(1)"
	}
	// MySQL reports unsigned ints as "bigint unsigned" already; ensure parens.
	return value
}
