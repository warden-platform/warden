package connector

import (
	"fmt"
	"strings"
)

// QueryTableRef is the resolved (schema, table) target for a query source. It
// is produced by the control plane from resource metadata; the renderer never
// accepts a raw table name from the Agent.
type QueryTableRef struct {
	Schema string
	Table  string
}

// RenderedQuery is a parameterized MySQL statement plus its driver arguments,
// in placeholder order.
type RenderedQuery struct {
	SQL  string
	Args []any
}

// RenderMySQLQuery turns a canonical plan plus binding values into a
// parameterized MySQL statement. resolveTable maps a resource id to its
// schema/table; every identifier comes from a validated AST node and every
// value becomes a `?` placeholder argument.
func RenderMySQLQuery(plan *QueryAST, bindings map[string]any, resolveTable func(resourceID string) (QueryTableRef, error)) (*RenderedQuery, error) {
	if plan == nil || len(plan.Sources) < 1 {
		return nil, fmt.Errorf("query plan has no sources")
	}
	tables := make(map[string]QueryTableRef, len(plan.Sources)+len(plan.Joins))
	for _, s := range plan.Sources {
		ref, err := resolveTable(s.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("resolve source %q: %w", s.Alias, err)
		}
		tables[s.Alias] = ref
	}
	for _, j := range plan.Joins {
		ref, err := resolveTable(j.Source.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("resolve join source %q: %w", j.Source.Alias, err)
		}
		tables[j.Source.Alias] = ref
	}

	var args []any
	var b strings.Builder
	b.WriteString("SELECT ")
	selectParts := make([]string, 0, len(plan.Select))
	for _, item := range plan.Select {
		selectParts = append(selectParts, renderMySQLSelect(item))
	}
	b.WriteString(strings.Join(selectParts, ", "))

	b.WriteString(" FROM ")
	b.WriteString(renderMySQLTable(tables[plan.Sources[0].Alias], plan.Sources[0].Alias))

	for _, j := range plan.Joins {
		b.WriteString(" ")
		b.WriteString(renderMySQLJoin(j, tables))
	}

	if plan.Where != nil {
		where, err := renderMySQLPredicate(plan.Where, bindings, &args, true)
		if err != nil {
			return nil, fmt.Errorf("where: %w", err)
		}
		b.WriteString(" WHERE ")
		b.WriteString(where)
	}

	if len(plan.GroupBy) > 0 {
		groupParts := make([]string, 0, len(plan.GroupBy))
		for _, field := range plan.GroupBy {
			groupParts = append(groupParts, renderMySQLField(field))
		}
		b.WriteString(" GROUP BY ")
		b.WriteString(strings.Join(groupParts, ", "))
	}

	if len(plan.Having) > 0 {
		havingParts := make([]string, 0, len(plan.Having))
		for _, item := range plan.Having {
			part, err := renderMySQLHaving(item, bindings, &args)
			if err != nil {
				return nil, fmt.Errorf("having: %w", err)
			}
			havingParts = append(havingParts, part)
		}
		b.WriteString(" HAVING ")
		b.WriteString(strings.Join(havingParts, " AND "))
	}

	if len(plan.OrderBy) > 0 {
		orderParts := make([]string, 0, len(plan.OrderBy))
		for _, item := range plan.OrderBy {
			orderParts = append(orderParts, quoteMySQLIdentifier(item.OutputAlias)+" "+item.Direction)
		}
		b.WriteString(" ORDER BY ")
		b.WriteString(strings.Join(orderParts, ", "))
	}

	if plan.Limit != nil {
		b.WriteString(" LIMIT ?")
		args = append(args, *plan.Limit)
	}

	return &RenderedQuery{SQL: b.String(), Args: args}, nil
}

func renderMySQLTable(ref QueryTableRef, alias string) string {
	return quoteMySQLIdentifier(ref.Schema) + "." + quoteMySQLIdentifier(ref.Table) + " AS " + quoteMySQLIdentifier(alias)
}

func renderMySQLJoin(join QueryJoin, tables map[string]QueryTableRef) string {
	onParts := make([]string, 0, len(join.On))
	for _, on := range join.On {
		onParts = append(onParts, renderMySQLField(on.Left)+" "+mysqlComparison(on.Operator)+" "+renderMySQLField(on.Right))
	}
	return join.Type + " JOIN " + renderMySQLTable(tables[join.Source.Alias], join.Source.Alias) +
		" ON " + strings.Join(onParts, " AND ")
}

func renderMySQLSelect(item QuerySelect) string {
	var expr string
	if item.Aggregate != "" {
		expr = renderMySQLAggregate(item.Aggregate, item.Distinct, item.Field)
	} else {
		expr = renderMySQLField(*item.Field)
	}
	return expr + " AS " + quoteMySQLIdentifier(item.Alias)
}

func renderMySQLAggregate(aggregate string, distinct bool, field *QueryField) string {
	if field == nil {
		return "COUNT(*)"
	}
	expr := renderMySQLField(*field)
	if distinct {
		expr = "DISTINCT " + expr
	}
	return aggregate + "(" + expr + ")"
}

func renderMySQLField(field QueryField) string {
	if field.Column == "*" {
		return "*"
	}
	return quoteMySQLIdentifier(field.Source) + "." + quoteMySQLIdentifier(field.Column)
}

func renderMySQLPredicate(p *QueryPredicate, bindings map[string]any, args *[]any, top bool) (string, error) {
	if p.Not != nil {
		inner, err := renderMySQLPredicate(p.Not, bindings, args, false)
		if err != nil {
			return "", err
		}
		return "NOT (" + inner + ")", nil
	}
	if p.And != nil || p.Or != nil {
		children := p.And
		joiner := " AND "
		if p.Or != nil {
			children = p.Or
			joiner = " OR "
		}
		parts := make([]string, 0, len(children))
		for i := range children {
			part, err := renderMySQLPredicate(&children[i], bindings, args, false)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		joined := strings.Join(parts, joiner)
		if top {
			return joined, nil
		}
		return "(" + joined + ")", nil
	}
	return renderMySQLLeaf(p, bindings, args)
}

func renderMySQLLeaf(p *QueryPredicate, bindings map[string]any, args *[]any) (string, error) {
	column := renderMySQLField(*p.Field)
	switch p.Operator {
	case "is_null":
		return column + " IS NULL", nil
	case "is_not_null":
		return column + " IS NOT NULL", nil
	case "in":
		value, err := leafBinding(p, bindings)
		if err != nil {
			return "", err
		}
		items, ok := value.([]any)
		if !ok {
			return "", fmt.Errorf("in binding must be an array")
		}
		placeholders := make([]string, 0, len(items))
		for _, item := range items {
			*args = append(*args, item)
			placeholders = append(placeholders, "?")
		}
		return column + " IN (" + strings.Join(placeholders, ",") + ")", nil
	default:
		value, err := leafBinding(p, bindings)
		if err != nil {
			return "", err
		}
		*args = append(*args, value)
		return column + " " + mysqlComparison(p.Operator) + " ?", nil
	}
}

func renderMySQLHaving(item QueryHaving, bindings map[string]any, args *[]any) (string, error) {
	expr := renderMySQLAggregate(item.Aggregate, item.Distinct, &item.Field)
	value, err := havingBinding(item, bindings)
	if err != nil {
		return "", err
	}
	*args = append(*args, value)
	return expr + " " + mysqlComparison(item.Operator) + " ?", nil
}

func leafBinding(p *QueryPredicate, bindings map[string]any) (any, error) {
	if p.Parameter != "" {
		value, ok := bindings[p.Parameter]
		if !ok {
			return nil, fmt.Errorf("parameter %q has no binding", p.Parameter)
		}
		return value, nil
	}
	return p.Value, nil
}

func havingBinding(item QueryHaving, bindings map[string]any) (any, error) {
	if item.Parameter != "" {
		value, ok := bindings[item.Parameter]
		if !ok {
			return nil, fmt.Errorf("parameter %q has no binding", item.Parameter)
		}
		return value, nil
	}
	return item.Value, nil
}

func mysqlComparison(op string) string {
	switch op {
	case "eq":
		return "="
	case "ne":
		return "<>"
	case "gt":
		return ">"
	case "gte":
		return ">="
	case "lt":
		return "<"
	case "lte":
		return "<="
	case "like":
		return "LIKE"
	default:
		return "="
	}
}

func quoteMySQLIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}
