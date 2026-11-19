package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// CanonicalizeQuery validates a query AST and returns its canonical plan plus
// a stable hash. The plan is the AST with all enums uppercased, order-insensitive
// slices sorted, and bindings stripped; the hash is sha256 over the plan's
// canonical JSON. Binding values are deliberately excluded from the hash so a
// preview and its apply share a plan identity even though the values differ
// (values are validated here but only short-lived / encrypted elsewhere).
func CanonicalizeQuery(ast *QueryAST) (*QueryAST, string, error) {
	if ast == nil {
		return nil, "", fmt.Errorf("query AST is nil")
	}
	if ast.Version == 0 {
		return nil, "", fmt.Errorf("astVersion is required and must be %d", QueryASTVersion)
	}
	if ast.Version != QueryASTVersion {
		return nil, "", fmt.Errorf("unsupported astVersion %d", ast.Version)
	}

	plan := &QueryAST{Version: ast.Version}

	if len(ast.Sources) < 1 || len(ast.Sources) > maxQuerySources {
		return nil, "", fmt.Errorf("sources must contain between 1 and %d tables", maxQuerySources)
	}
	aliases := make(map[string]bool, len(ast.Sources))
	resources := make(map[string]bool, len(ast.Sources))
	for _, s := range ast.Sources {
		if s.Alias == "" || len(s.Alias) > 64 {
			return nil, "", fmt.Errorf("source alias must be a non-empty identifier of at most 64 bytes")
		}
		if s.ResourceID == "" || len(s.ResourceID) > 128 {
			return nil, "", fmt.Errorf("source resourceId is required")
		}
		if aliases[s.Alias] {
			return nil, "", fmt.Errorf("duplicate source alias %q", s.Alias)
		}
		if resources[s.ResourceID] {
			return nil, "", fmt.Errorf("duplicate source resourceId %q", s.ResourceID)
		}
		aliases[s.Alias] = true
		resources[s.ResourceID] = true
		plan.Sources = append(plan.Sources, s)
	}

	if len(ast.Joins) > maxQueryJoins {
		return nil, "", fmt.Errorf("joins must contain at most %d clauses", maxQueryJoins)
	}
	for _, j := range ast.Joins {
		joinType := strings.ToUpper(strings.TrimSpace(j.Type))
		if joinType != "INNER" && joinType != "LEFT" {
			return nil, "", fmt.Errorf("unsupported join type %q", j.Type)
		}
		if j.Source.Alias == "" || len(j.Source.Alias) > 64 {
			return nil, "", fmt.Errorf("join source alias must be a non-empty identifier")
		}
		if j.Source.ResourceID == "" || len(j.Source.ResourceID) > 128 {
			return nil, "", fmt.Errorf("join source resourceId is required")
		}
		if aliases[j.Source.Alias] {
			return nil, "", fmt.Errorf("duplicate source alias %q", j.Source.Alias)
		}
		if resources[j.Source.ResourceID] {
			return nil, "", fmt.Errorf("duplicate source resourceId %q", j.Source.ResourceID)
		}
		aliases[j.Source.Alias] = true
		resources[j.Source.ResourceID] = true
		if len(j.On) < 1 || len(j.On) > maxQueryLogicalBranches {
			return nil, "", fmt.Errorf("join on must contain between 1 and %d conditions", maxQueryLogicalBranches)
		}
		normalized := QueryJoin{Type: joinType, Source: j.Source}
		for _, on := range j.On {
			if err := validateQueryField(on.Left, aliases); err != nil {
				return nil, "", fmt.Errorf("join on: %w", err)
			}
			if err := validateQueryField(on.Right, aliases); err != nil {
				return nil, "", fmt.Errorf("join on: %w", err)
			}
			op := strings.ToLower(on.Operator)
			switch op {
			case "eq", "ne", "gt", "gte", "lt", "lte":
			default:
				return nil, "", fmt.Errorf("unsupported join operator %q", on.Operator)
			}
			normalized.On = append(normalized.On, QueryJoinOn{Left: on.Left, Operator: op, Right: on.Right})
		}
		plan.Joins = append(plan.Joins, normalized)
	}

	if len(ast.Select) < 1 || len(ast.Select) > maxQuerySelectItems {
		return nil, "", fmt.Errorf("select must contain between 1 and %d items", maxQuerySelectItems)
	}
	selectAliases := make(map[string]bool, len(ast.Select))
	for _, item := range ast.Select {
		if item.Alias == "" || len(item.Alias) > 64 {
			return nil, "", fmt.Errorf("select alias must be a non-empty identifier of at most 64 bytes")
		}
		if selectAliases[item.Alias] {
			return nil, "", fmt.Errorf("duplicate select alias %q", item.Alias)
		}
		selectAliases[item.Alias] = true
		normalized := QuerySelect{Alias: item.Alias}
		if item.Aggregate == "" {
			if item.Distinct {
				return nil, "", fmt.Errorf("distinct is only valid with an aggregate")
			}
			if item.Field == nil {
				return nil, "", fmt.Errorf("plain select item %q requires a field", item.Alias)
			}
			if err := validateQueryField(*item.Field, aliases); err != nil {
				return nil, "", fmt.Errorf("select %q: %w", item.Alias, err)
			}
			normalized.Field = item.Field
		} else {
			agg := strings.ToUpper(item.Aggregate)
			if !queryAggregates[agg] {
				return nil, "", fmt.Errorf("unsupported aggregate %q", item.Aggregate)
			}
			normalized.Aggregate = agg
			normalized.Distinct = item.Distinct
			if agg == "COUNT" && item.Field == nil {
				if item.Distinct {
					return nil, "", fmt.Errorf("COUNT(DISTINCT) requires a field")
				}
			} else {
				if item.Field == nil {
					return nil, "", fmt.Errorf("aggregate %s requires a field", agg)
				}
				star := item.Field.Column == "*"
				if star && (agg != "COUNT" || item.Distinct) {
					return nil, "", fmt.Errorf("column * is only allowed for a plain COUNT")
				}
				if err := validateQueryField(*item.Field, aliases); err != nil {
					return nil, "", fmt.Errorf("select %q: %w", item.Alias, err)
				}
				normalized.Field = item.Field
			}
		}
		plan.Select = append(plan.Select, normalized)
	}

	if len(ast.GroupBy) > maxQueryGroupBy {
		return nil, "", fmt.Errorf("groupBy must contain at most %d items", maxQueryGroupBy)
	}
	for _, field := range ast.GroupBy {
		if err := validateQueryField(field, aliases); err != nil {
			return nil, "", fmt.Errorf("groupBy: %w", err)
		}
		plan.GroupBy = append(plan.GroupBy, field)
	}

	if len(ast.Having) > maxQueryHaving {
		return nil, "", fmt.Errorf("having must contain at most %d items", maxQueryHaving)
	}
	for _, item := range ast.Having {
		agg := strings.ToUpper(item.Aggregate)
		if !queryAggregates[agg] {
			return nil, "", fmt.Errorf("unsupported having aggregate %q", item.Aggregate)
		}
		if item.Field.Column == "*" {
			return nil, "", fmt.Errorf("having aggregate %s requires a concrete field", agg)
		}
		if err := validateQueryField(item.Field, aliases); err != nil {
			return nil, "", fmt.Errorf("having: %w", err)
		}
		op := strings.ToLower(item.Operator)
		if !queryComparisonOperators[op] {
			return nil, "", fmt.Errorf("unsupported having operator %q", item.Operator)
		}
		normalized := QueryHaving{Aggregate: agg, Distinct: item.Distinct, Field: item.Field, Operator: op}
		if err := bindLeafValue(&normalized.Parameter, &normalized.Value, item.Parameter, item.Value, op, ast.Bindings); err != nil {
			return nil, "", fmt.Errorf("having: %w", err)
		}
		plan.Having = append(plan.Having, normalized)
	}

	if ast.Where != nil {
		normalized, err := normalizePredicate(ast.Where, aliases, ast.Bindings, 1)
		if err != nil {
			return nil, "", fmt.Errorf("where: %w", err)
		}
		plan.Where = normalized
	}

	if len(ast.OrderBy) > maxQueryOrderBy {
		return nil, "", fmt.Errorf("orderBy must contain at most %d items", maxQueryOrderBy)
	}
	for _, item := range ast.OrderBy {
		if !selectAliases[item.OutputAlias] {
			return nil, "", fmt.Errorf("orderBy references unknown output alias %q", item.OutputAlias)
		}
		dir := strings.ToUpper(item.Direction)
		if dir != "ASC" && dir != "DESC" {
			return nil, "", fmt.Errorf("unsupported orderBy direction %q", item.Direction)
		}
		plan.OrderBy = append(plan.OrderBy, QueryOrder{OutputAlias: item.OutputAlias, Direction: dir})
	}

	if ast.Limit != nil {
		if *ast.Limit < 1 || *ast.Limit > maxQueryLimit {
			return nil, "", fmt.Errorf("limit must be between 1 and %d", maxQueryLimit)
		}
		limit := *ast.Limit
		plan.Limit = &limit
	}

	if len(ast.Bindings) > maxQueryBindings {
		return nil, "", fmt.Errorf("bindings must contain at most %d entries", maxQueryBindings)
	}

	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical plan: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return plan, hex.EncodeToString(sum[:]), nil
}

// bindLeafValue validates a (parameter XOR value) leaf binding and copies it
// into the normalized fields. parameter and value are mutually exclusive; for
// is_null/is_not_null both are empty.
func bindLeafValue(dstParam *string, dstValue *any, parameter string, value any, op string, bindings map[string]any) error {
	switch op {
	case "is_null", "is_not_null":
		if parameter != "" || value != nil {
			return fmt.Errorf("operator %s must not bind a value", op)
		}
		return nil
	}
	hasParam := parameter != ""
	hasValue := value != nil
	if hasParam == hasValue {
		return fmt.Errorf("leaf must bind exactly one of parameter or value")
	}
	if hasParam {
		if len(parameter) > 128 {
			return fmt.Errorf("parameter name too long")
		}
		binding, ok := bindings[parameter]
		if !ok {
			return fmt.Errorf("parameter %q has no binding", parameter)
		}
		if err := validateQueryLiteral(binding, op); err != nil {
			return fmt.Errorf("parameter %q: %w", parameter, err)
		}
		*dstParam = parameter
		return nil
	}
	if err := validateQueryLiteral(value, op); err != nil {
		return err
	}
	*dstValue = value
	return nil
}

func normalizePredicate(p *QueryPredicate, aliases map[string]bool, bindings map[string]any, depth int) (*QueryPredicate, error) {
	if p == nil {
		return nil, fmt.Errorf("empty predicate")
	}
	if depth > maxQueryPredicateDepth {
		return nil, fmt.Errorf("predicate depth exceeds %d", maxQueryPredicateDepth)
	}
	logical := 0
	if p.And != nil {
		logical++
	}
	if p.Or != nil {
		logical++
	}
	if p.Not != nil {
		logical++
	}
	leaf := p.Field != nil
	if logical > 0 && leaf {
		return nil, fmt.Errorf("predicate cannot be both logical and a field comparison")
	}
	if logical > 1 {
		return nil, fmt.Errorf("predicate must use exactly one of and/or/not")
	}
	if logical == 0 && !leaf {
		return nil, fmt.Errorf("empty predicate node")
	}

	if leaf {
		if p.Operator == "" {
			return nil, fmt.Errorf("field predicate requires an operator")
		}
		op := strings.ToLower(p.Operator)
		if !queryOperators[op] {
			return nil, fmt.Errorf("unsupported operator %q", p.Operator)
		}
		if err := validateQueryField(*p.Field, aliases); err != nil {
			return nil, err
		}
		normalized := &QueryPredicate{Field: p.Field, Operator: op}
		if err := bindLeafValue(&normalized.Parameter, &normalized.Value, p.Parameter, p.Value, op, bindings); err != nil {
			return nil, err
		}
		return normalized, nil
	}

	normalized := &QueryPredicate{}
	if p.Not != nil {
		child, err := normalizePredicate(p.Not, aliases, bindings, depth+1)
		if err != nil {
			return nil, err
		}
		normalized.Not = child
		return normalized, nil
	}
	children := p.And
	if p.Or != nil {
		children = p.Or
	}
	if len(children) < 1 || len(children) > maxQueryLogicalBranches {
		return nil, fmt.Errorf("logical predicate must contain between 1 and %d children", maxQueryLogicalBranches)
	}
	normalizedChildren := make([]QueryPredicate, 0, len(children))
	for i := range children {
		child, err := normalizePredicate(&children[i], aliases, bindings, depth+1)
		if err != nil {
			return nil, err
		}
		normalizedChildren = append(normalizedChildren, *child)
	}
	if p.Or != nil {
		normalized.Or = normalizedChildren
	} else {
		normalized.And = normalizedChildren
	}
	return normalized, nil
}

func validateQueryField(field QueryField, aliases map[string]bool) error {
	if !aliases[field.Source] {
		return fmt.Errorf("field references unknown source alias %q", field.Source)
	}
	if field.Column == "" || len(field.Column) > 128 {
		return fmt.Errorf("field column must be a non-empty identifier of at most 128 bytes")
	}
	return nil
}

func validateQueryLiteral(value any, op string) error {
	switch op {
	case "in":
		items, ok := value.([]any)
		if !ok || len(items) < 1 || len(items) > maxQueryInValues {
			return fmt.Errorf("in requires between 1 and %d values", maxQueryInValues)
		}
		for _, item := range items {
			if !queryScalar(item) {
				return fmt.Errorf("in values must be strings, numbers or booleans")
			}
		}
		return nil
	default:
		if !queryScalar(value) {
			return fmt.Errorf("value must be a string, number or boolean")
		}
		return nil
	}
}

func queryScalar(value any) bool {
	switch value.(type) {
	case string, bool, float64, int, int32, int64, json.Number:
		return true
	default:
		return false
	}
}
