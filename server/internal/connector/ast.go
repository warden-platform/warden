package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Query AST — the single execution kernel for governed cross-table queries
// (database.query.preview / database.query.execute / templates). A structured
// AST, not raw SQL, is what the Agent submits and what the control plane
// authorizes: identifiers and functions can only come from validated nodes,
// and every literal value is bound into a driver parameter. See design §4.3.

const QueryASTVersion = 1

// Static limits (§12.1). These bound the work a single governed query can
// request before it ever reaches the database.
const (
	maxQuerySources        = 10  // distinct base tables
	maxQueryJoins          = 8   // join clauses (inner + left)
	maxQuerySelectItems    = 50  // output columns / aggregate slots
	maxQueryGroupBy        = 20  // group-by expressions
	maxQueryHaving         = 20  // having expressions
	maxQueryOrderBy        = 10  // sort keys
	maxQueryPredicateDepth = 8   // where/on/having nesting depth
	maxQueryLogicalBranches = 12 // children of a single and/or node
	maxQueryInValues       = 100 // values in an IN list
	maxQueryLimit          = 1000
	maxQueryBindings       = 100
)

// QueryAST is the top-level query node. JSON tags mirror the design §4.3 wire
// shape; decoding is strict (unknown fields are rejected), so the tags are the
// contract.
type QueryAST struct {
	Version  int            `json:"astVersion"`
	Sources  []QuerySource  `json:"sources"`
	Joins    []QueryJoin    `json:"joins,omitempty"`
	Select   []QuerySelect  `json:"select"`
	Where    *QueryPredicate `json:"where,omitempty"`
	GroupBy  []QueryField   `json:"groupBy,omitempty"`
	Having   []QueryHaving  `json:"having,omitempty"`
	OrderBy  []QueryOrder   `json:"orderBy,omitempty"`
	Limit    *int           `json:"limit,omitempty"`
	Bindings map[string]any `json:"bindings,omitempty"`
}

// QuerySource declares a base table under a query-local alias. resourceId is a
// PAM resource id (resolved to schema/table only at render time by the control
// plane), never a raw table name typed by the Agent.
type QuerySource struct {
	Alias      string `json:"alias"`
	ResourceID string `json:"resourceId"`
}

// QueryField is a (source, column) reference. column may be "*" only for a
// COUNT aggregate; otherwise it must name a concrete column.
type QueryField struct {
	Source string `json:"source"`
	Column string `json:"column"`
}

// QueryJoin is a single join clause. Only INNER and LEFT are supported (§4.4).
type QueryJoin struct {
	Type   string            `json:"type"`
	Source QuerySource       `json:"source"`
	On     []QueryJoinOn     `json:"on"`
}

// QueryJoinOn is one equi-join condition between two query fields.
type QueryJoinOn struct {
	Left     QueryField `json:"left"`
	Operator string     `json:"operator"`
	Right    QueryField `json:"right"`
}

// QuerySelect is one output slot: either a plain column or an aggregate.
type QuerySelect struct {
	Field     *QueryField `json:"field,omitempty"`
	Aggregate string      `json:"aggregate,omitempty"`
	Distinct  bool        `json:"distinct,omitempty"`
	Alias     string      `json:"alias"`
}

// QueryHaving is an aggregate filter applied after GROUP BY.
type QueryHaving struct {
	Aggregate string     `json:"aggregate"`
	Distinct  bool       `json:"distinct,omitempty"`
	Field     QueryField `json:"field"`
	Operator  string     `json:"operator"`
	Parameter string     `json:"parameter,omitempty"`
	Value     any        `json:"value,omitempty"`
}

// QueryOrder is a sort key referencing an output alias.
type QueryOrder struct {
	OutputAlias string `json:"outputAlias"`
	Direction   string `json:"direction"`
}

// QueryPredicate is a bounded boolean expression tree. A leaf binds a field to
// either a named parameter (resolved against bindings) or an inline literal.
// Logical nodes carry exactly one of and/or/not.
type QueryPredicate struct {
	And       []QueryPredicate `json:"and,omitempty"`
	Or        []QueryPredicate `json:"or,omitempty"`
	Not       *QueryPredicate  `json:"not,omitempty"`
	Field     *QueryField      `json:"field,omitempty"`
	Operator  string           `json:"operator,omitempty"`
	Parameter string           `json:"parameter,omitempty"`
	Value     any              `json:"value,omitempty"`
}

// queryOperators is the leaf-operator allowlist shared by where, having and on.
var queryOperators = map[string]bool{
	"eq": true, "ne": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"like": true, "in": true, "is_null": true, "is_not_null": true,
}

// queryAggregates is the aggregate allowlist (§4.4). COUNT DISTINCT is encoded
// as aggregate=COUNT with distinct=true.
var queryAggregates = map[string]bool{
	"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true,
}

// queryComparisonOperators is the comparison allowlist for HAVING (aggregate
// filters); IN/LIKE/null-tests are not meaningful against an aggregate.
var queryComparisonOperators = map[string]bool{
	"eq": true, "ne": true, "gt": true, "gte": true, "lt": true, "lte": true,
}

// decodeQueryAST parses a query AST from its wire JSON with unknown-field
// rejection and a trailing-garbage guard. It performs no semantic validation;
// that is Canonicalize's job.
func decodeQueryAST(data []byte) (*QueryAST, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var ast QueryAST
	if err := dec.Decode(&ast); err != nil {
		return nil, fmt.Errorf("decode query AST: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("decode query AST: unexpected trailing data")
	}
	return &ast, nil
}
