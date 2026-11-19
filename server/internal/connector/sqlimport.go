package connector

import (
	"fmt"
	"strconv"
	"strings"
)

// SQL import (§6). A pinned, in-tree parser converts an administrator's
// existing MySQL SELECT into the same bounded Query AST the runtime executes —
// never raw SQL. It accepts only the subset the AST supports (INNER/LEFT JOIN,
// the five aggregates, GROUP BY/HAVING, ORDER BY, LIMIT, bounded AND/OR/NOT
// predicates, `:name` parameters or literals) and rejects everything else,
// including multi-statement input, DML/DDL, subqueries, UNION, RIGHT/FULL/CROSS
// JOIN, window functions, user variables, system schemas and any function
// outside the aggregate whitelist (which also covers SLEEP/BENCHMARK/LOAD_FILE/
// GET_LOCK/RELEASE_LOCK and friends as unknown functions).
//
// The result is an ImportedQuery whose AST has table aliases resolved but
// resource ids unbound (or bound to table/<schema>/<table> when the table is
// schema-qualified); the control plane binds the remaining resource ids and
// then the AST flows into a query template for human review and publish.

// ImportedTable is a table reference an administrator named in FROM/JOIN, to be
// bound to a Warden database_table resource by the control plane.
type ImportedTable struct {
	Schema string `json:"schema,omitempty"`
	Table  string `json:"table"`
	Alias  string `json:"alias"`
}

// ImportedQuery is the parse product: a draft AST plus the table references and
// named parameters that still need binding.
type ImportedQuery struct {
	AST        *QueryAST       `json:"ast"`
	Tables     []ImportedTable `json:"tables"`
	Parameters []string        `json:"parameters"`
}

// sqlTokKind enumerates lexer tokens.
type sqlTokKind int

const (
	sqlEOF sqlTokKind = iota
	sqlIdent
	sqlString
	sqlNumber
	sqlParam
	sqlPunct
)

type sqlTok struct {
	kind sqlTokKind
	text string
	pos  int
}

// sqlKeywords is the set of names that are keywords in this grammar and are
// therefore never treated as columns, tables or function calls.
var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "JOIN": true, "INNER": true, "LEFT": true,
	"RIGHT": true, "FULL": true, "CROSS": true, "ON": true, "WHERE": true,
	"GROUP": true, "HAVING": true, "ORDER": true, "LIMIT": true, "BY": true,
	"AS": true, "AND": true, "OR": true, "NOT": true, "IN": true, "IS": true,
	"LIKE": true, "ASC": true, "DESC": true, "DISTINCT": true, "UNION": true,
	"OFFSET": true, "OUTER": true, "TRUE": true, "FALSE": true, "NULL": true,
	"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true,
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '$'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAggregateName(s string) bool {
	switch strings.ToUpper(s) {
	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		return true
	}
	return false
}

func isSystemSchema(s string) bool {
	switch strings.ToLower(s) {
	case "information_schema", "mysql", "performance_schema", "sys":
		return true
	}
	return false
}

func isReservedAliasKeyword(s string) bool {
	switch strings.ToUpper(s) {
	case "SELECT", "FROM", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS",
		"ON", "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "BY", "AS", "AND",
		"OR", "NOT", "IN", "IS", "LIKE", "ASC", "DESC", "DISTINCT", "UNION",
		"OFFSET", "OUTER":
		return true
	}
	return false
}

func lexSQL(input string) ([]sqlTok, error) {
	toks := make([]sqlTok, 0, 32)
	for i := 0; i < len(input); {
		c := input[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == ';':
			return nil, fmt.Errorf("sql import: multiple statements are not supported")
		case c == '@':
			return nil, fmt.Errorf("sql import: user variables are not supported")
		case c == '`':
			text, next, err := lexQuoted(input, i, '`')
			if err != nil {
				return nil, err
			}
			toks = append(toks, sqlTok{kind: sqlIdent, text: text, pos: i})
			i = next
		case c == '\'':
			text, next, err := lexQuoted(input, i, '\'')
			if err != nil {
				return nil, err
			}
			toks = append(toks, sqlTok{kind: sqlString, text: text, pos: i})
			i = next
		case c == ':':
			j := i + 1
			start := j
			for j < len(input) && isIdentChar(input[j]) {
				j++
			}
			if j == start {
				return nil, fmt.Errorf("sql import: empty parameter name at position %d", i)
			}
			toks = append(toks, sqlTok{kind: sqlParam, text: input[start:j], pos: i})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(input) && (isDigit(input[j]) || input[j] == '.' || input[j] == 'e' || input[j] == 'E') {
				j++
			}
			if j < len(input) && (input[j] == '+' || input[j] == '-') && j > i && (input[j-1] == 'e' || input[j-1] == 'E') {
				j++
				for j < len(input) && isDigit(input[j]) {
					j++
				}
			}
			toks = append(toks, sqlTok{kind: sqlNumber, text: input[i:j], pos: i})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(input) && isIdentChar(input[j]) {
				j++
			}
			toks = append(toks, sqlTok{kind: sqlIdent, text: input[i:j], pos: i})
			i = j
		default:
			two := ""
			if i+1 < len(input) {
				two = input[i : i+2]
			}
			switch two {
			case "<=", ">=", "<>", "!=":
				toks = append(toks, sqlTok{kind: sqlPunct, text: two, pos: i})
				i += 2
				continue
			}
			switch c {
			case '(', ')', ',', '.', '*', '=', '<', '>':
				toks = append(toks, sqlTok{kind: sqlPunct, text: string(c), pos: i})
				i++
			default:
				return nil, fmt.Errorf("sql import: unexpected character %q at position %d", string(c), i)
			}
		}
	}
	toks = append(toks, sqlTok{kind: sqlEOF, pos: len(input)})
	return toks, nil
}

func lexQuoted(input string, start int, quote byte) (string, int, error) {
	var b strings.Builder
	i := start + 1
	for i < len(input) {
		if input[i] == quote {
			if i+1 < len(input) && input[i+1] == quote {
				b.WriteByte(quote)
				i += 2
				continue
			}
			return b.String(), i + 1, nil
		}
		b.WriteByte(input[i])
		i++
	}
	kind := "backtick identifier"
	if quote == '\'' {
		kind = "string literal"
	}
	return "", 0, fmt.Errorf("sql import: unterminated %s at position %d", kind, start)
}

// rejectForbiddenCalls enforces the function whitelist: any identifier
// immediately followed by `(` that is not one of the five aggregates is an
// unknown/forbidden function (SLEEP, BENCHMARK, LOAD_FILE, GET_LOCK,
// RELEASE_LOCK, IFNULL, NOW, …).
func rejectForbiddenCalls(toks []sqlTok) error {
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].kind == sqlIdent && toks[i+1].kind == sqlPunct && toks[i+1].text == "(" {
			up := strings.ToUpper(toks[i].text)
			if !sqlKeywords[up] && !isAggregateName(up) {
				return fmt.Errorf("sql import: unsupported function %q at position %d", toks[i].text, toks[i].pos)
			}
		}
	}
	return nil
}

// ImportQuerySQL parses an administrator's MySQL SELECT into a draft Query AST.
func ImportQuerySQL(sql string) (*ImportedQuery, error) {
	toks, err := lexSQL(sql)
	if err != nil {
		return nil, err
	}
	if err := rejectForbiddenCalls(toks); err != nil {
		return nil, err
	}
	p := &sqlImportParser{toks: toks, aliases: map[string]bool{}, params: map[string]bool{}}
	return p.parse()
}

type sqlImportParser struct {
	toks    []sqlTok
	i       int
	aliases map[string]bool
	params  map[string]bool
	tables  []ImportedTable
}

func (p *sqlImportParser) cur() sqlTok { return p.toks[p.i] }
func (p *sqlImportParser) atEnd() bool { return p.cur().kind == sqlEOF }
func (p *sqlImportParser) advance() sqlTok {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}
func (p *sqlImportParser) errf(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("sql import: %s at position %d", msg, p.cur().pos)
}

func (p *sqlImportParser) isKeyword(t sqlTok, kw string) bool {
	return t.kind == sqlIdent && strings.EqualFold(t.text, kw)
}
func (p *sqlImportParser) matchKeyword(kw string) bool {
	if p.isKeyword(p.cur(), kw) {
		p.advance()
		return true
	}
	return false
}
func (p *sqlImportParser) expectKeyword(kw string) error {
	if !p.isKeyword(p.cur(), kw) {
		return p.errf("expected %s, got %q", kw, p.cur().text)
	}
	p.advance()
	return nil
}
func (p *sqlImportParser) expectPunct(s string) error {
	t := p.cur()
	if t.kind != sqlPunct || t.text != s {
		return p.errf("expected %q, got %q", s, t.text)
	}
	p.advance()
	return nil
}

// importSelect is an intermediate select-item representation before bare-column
// sources are resolved against the FROM aliases.
type importSelect struct {
	aggregate string
	distinct  bool
	field     *QueryField
	alias     string
}

func (p *sqlImportParser) parse() (*ImportedQuery, error) {
	if err := p.expectKeyword("select"); err != nil {
		return nil, err
	}
	items, err := p.parseSelectList()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("from"); err != nil {
		return nil, err
	}
	first, err := p.parseTableRef()
	if err != nil {
		return nil, err
	}
	sources := []QuerySource{first}
	var joins []QueryJoin
	for p.isKeyword(p.cur(), "join") || p.isKeyword(p.cur(), "inner") || p.isKeyword(p.cur(), "left") {
		join, err := p.parseJoin()
		if err != nil {
			return nil, err
		}
		joins = append(joins, join)
	}
	if p.isKeyword(p.cur(), "right") || p.isKeyword(p.cur(), "full") || p.isKeyword(p.cur(), "cross") {
		return nil, p.errf("only INNER and LEFT JOIN are supported")
	}
	var where *QueryPredicate
	if p.matchKeyword("where") {
		where, err = p.parseOr()
		if err != nil {
			return nil, err
		}
	}
	var groupBy []QueryField
	if p.isKeyword(p.cur(), "group") {
		p.advance()
		if err := p.expectKeyword("by"); err != nil {
			return nil, err
		}
		groupBy, err = p.parseFieldList()
		if err != nil {
			return nil, err
		}
	}
	var having []QueryHaving
	if p.matchKeyword("having") {
		having, err = p.parseHavingList()
		if err != nil {
			return nil, err
		}
	}
	var orderBy []QueryOrder
	if p.isKeyword(p.cur(), "order") {
		p.advance()
		if err := p.expectKeyword("by"); err != nil {
			return nil, err
		}
		orderBy, err = p.parseOrderBy()
		if err != nil {
			return nil, err
		}
	}
	var limit *int
	if p.matchKeyword("limit") {
		limit, err = p.parseLimit()
		if err != nil {
			return nil, err
		}
	}
	if !p.atEnd() {
		return nil, p.errf("unexpected trailing input %q", p.cur().text)
	}

	selects := make([]QuerySelect, 0, len(items))
	for _, item := range items {
		selects = append(selects, QuerySelect{
			Field:     item.field,
			Aggregate: item.aggregate,
			Distinct:  item.distinct,
			Alias:     item.alias,
		})
	}
	ast := &QueryAST{
		Version: QueryASTVersion,
		Sources: sources,
		Joins:   joins,
		Select:  selects,
		Where:   where,
		GroupBy: groupBy,
		Having:  having,
		OrderBy: orderBy,
		Limit:   limit,
	}
	if err := resolveASTFields(ast, p.aliases); err != nil {
		return nil, err
	}
	params := make([]string, 0, len(p.params))
	for name := range p.params {
		params = append(params, name)
	}
	return &ImportedQuery{AST: ast, Tables: p.tables, Parameters: params}, nil
}

func (p *sqlImportParser) parseSelectList() ([]importSelect, error) {
	var out []importSelect
	for {
		item, err := p.parseSelectItem()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
		if !(p.cur().kind == sqlPunct && p.cur().text == ",") {
			break
		}
		p.advance()
	}
	return out, nil
}

func (p *sqlImportParser) parseSelectItem() (importSelect, error) {
	if p.cur().kind == sqlPunct && p.cur().text == "*" {
		return importSelect{}, p.errf("SELECT * is not supported; name columns explicitly")
	}
	if p.cur().kind == sqlIdent && isAggregateName(p.cur().text) {
		agg := strings.ToUpper(p.advance().text)
		if err := p.expectPunct("("); err != nil {
			return importSelect{}, err
		}
		distinct := p.matchKeyword("distinct")
		var field *QueryField
		if p.cur().kind == sqlPunct && p.cur().text == "*" {
			if agg != "COUNT" {
				return importSelect{}, p.errf("%s(*) is not supported", agg)
			}
			p.advance()
		} else {
			f, err := p.parseQualifiedField(false)
			if err != nil {
				return importSelect{}, err
			}
			field = &f
		}
		if err := p.expectPunct(")"); err != nil {
			return importSelect{}, err
		}
		alias, err := p.parseOptionalAlias()
		if err != nil {
			return importSelect{}, err
		}
		if alias == "" {
			alias = defaultAggregateAlias(agg, field)
		}
		return importSelect{aggregate: agg, distinct: distinct, field: field, alias: alias}, nil
	}
	f, err := p.parseQualifiedField(false)
	if err != nil {
		return importSelect{}, err
	}
	alias, err := p.parseOptionalAlias()
	if err != nil {
		return importSelect{}, err
	}
	if alias == "" {
		alias = f.Column
	}
	return importSelect{field: &f, alias: alias}, nil
}

func defaultAggregateAlias(agg string, field *QueryField) string {
	col := "all"
	if field != nil {
		col = field.Column
		if field.Source != "" {
			col = field.Source + "_" + col
		}
	}
	return strings.ToLower(agg) + "_" + col
}

func (p *sqlImportParser) parseTableRef() (QuerySource, error) {
	t := p.cur()
	if t.kind != sqlIdent {
		return QuerySource{}, p.errf("expected table name, got %q", t.text)
	}
	first := t.text
	p.advance()
	schema, table := "", first
	if p.cur().kind == sqlPunct && p.cur().text == "." {
		p.advance()
		second := p.cur()
		if second.kind != sqlIdent {
			return QuerySource{}, p.errf("expected table name after schema %q", first)
		}
		schema, table = first, second.text
		p.advance()
	}
	if isSystemSchema(schema) {
		return QuerySource{}, fmt.Errorf("sql import: system schema %q is not allowed", schema)
	}
	alias, err := p.parseOptionalAlias()
	if err != nil {
		return QuerySource{}, err
	}
	if alias == "" {
		alias = table
	}
	if err := p.registerAlias(alias); err != nil {
		return QuerySource{}, err
	}
	p.tables = append(p.tables, ImportedTable{Schema: schema, Table: table, Alias: alias})
	return QuerySource{Alias: alias, ResourceID: resourceIDFromTable(schema, table)}, nil
}

func resourceIDFromTable(schema, table string) string {
	if schema == "" {
		return ""
	}
	return "table/" + schema + "/" + table
}

func (p *sqlImportParser) registerAlias(alias string) error {
	if p.aliases[alias] {
		return fmt.Errorf("sql import: duplicate table alias %q", alias)
	}
	p.aliases[alias] = true
	return nil
}

func (p *sqlImportParser) parseOptionalAlias() (string, error) {
	if p.matchKeyword("as") {
		t := p.cur()
		if t.kind != sqlIdent {
			return "", p.errf("expected alias after AS, got %q", t.text)
		}
		p.advance()
		if isReservedAliasKeyword(t.text) {
			return "", p.errf("%q is a reserved word and cannot be an alias", t.text)
		}
		return t.text, nil
	}
	t := p.cur()
	if t.kind == sqlIdent && !isReservedAliasKeyword(t.text) {
		p.advance()
		return t.text, nil
	}
	return "", nil
}

func (p *sqlImportParser) parseJoin() (QueryJoin, error) {
	joinType := "INNER"
	if p.matchKeyword("inner") {
		// INNER JOIN
	} else if p.matchKeyword("left") {
		joinType = "LEFT"
		p.matchKeyword("outer")
	}
	if err := p.expectKeyword("join"); err != nil {
		return QueryJoin{}, err
	}
	src, err := p.parseTableRef()
	if err != nil {
		return QueryJoin{}, err
	}
	if err := p.expectKeyword("on"); err != nil {
		return QueryJoin{}, err
	}
	var conditions []QueryJoinOn
	for {
		left, err := p.parseQualifiedField(true)
		if err != nil {
			return QueryJoin{}, err
		}
		op, err := p.parseOrderedOperator()
		if err != nil {
			return QueryJoin{}, err
		}
		right, err := p.parseQualifiedField(true)
		if err != nil {
			return QueryJoin{}, err
		}
		conditions = append(conditions, QueryJoinOn{Left: left, Operator: op, Right: right})
		if !p.matchKeyword("and") {
			break
		}
	}
	return QueryJoin{Type: joinType, Source: src, On: conditions}, nil
}

func (p *sqlImportParser) parseQualifiedField(requireQualified bool) (QueryField, error) {
	t := p.cur()
	if t.kind != sqlIdent {
		return QueryField{}, p.errf("expected column name, got %q", t.text)
	}
	first := t.text
	p.advance()
	if p.cur().kind == sqlPunct && p.cur().text == "." {
		p.advance()
		second := p.cur()
		if second.kind != sqlIdent {
			return QueryField{}, p.errf("expected column name after %q", first)
		}
		p.advance()
		return QueryField{Source: first, Column: second.text}, nil
	}
	if requireQualified {
		return QueryField{}, p.errf("column %q must be qualified with a table alias", first)
	}
	return QueryField{Source: "", Column: first}, nil
}

func (p *sqlImportParser) parseFieldList() ([]QueryField, error) {
	var out []QueryField
	for {
		f, err := p.parseQualifiedField(false)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		if !(p.cur().kind == sqlPunct && p.cur().text == ",") {
			break
		}
		p.advance()
	}
	return out, nil
}

func (p *sqlImportParser) parseHavingList() ([]QueryHaving, error) {
	var out []QueryHaving
	for {
		item, err := p.parseHavingItem()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
		if !p.matchKeyword("and") {
			break
		}
	}
	return out, nil
}

func (p *sqlImportParser) parseHavingItem() (QueryHaving, error) {
	if p.cur().kind != sqlIdent || !isAggregateName(p.cur().text) {
		return QueryHaving{}, p.errf("HAVING requires an aggregate comparison")
	}
	agg := strings.ToUpper(p.advance().text)
	if err := p.expectPunct("("); err != nil {
		return QueryHaving{}, err
	}
	distinct := p.matchKeyword("distinct")
	f, err := p.parseQualifiedField(false)
	if err != nil {
		return QueryHaving{}, err
	}
	if err := p.expectPunct(")"); err != nil {
		return QueryHaving{}, err
	}
	op, err := p.parseOrderedOperator()
	if err != nil {
		return QueryHaving{}, err
	}
	param, value, err := p.parseValue()
	if err != nil {
		return QueryHaving{}, err
	}
	return QueryHaving{Aggregate: agg, Distinct: distinct, Field: f, Operator: op, Parameter: param, Value: value}, nil
}

func (p *sqlImportParser) parseOrderBy() ([]QueryOrder, error) {
	var out []QueryOrder
	for {
		t := p.cur()
		if t.kind != sqlIdent {
			return nil, p.errf("ORDER BY must reference an output alias, got %q", t.text)
		}
		alias := t.text
		p.advance()
		dir := "ASC"
		if p.matchKeyword("desc") {
			dir = "DESC"
		} else {
			p.matchKeyword("asc")
		}
		out = append(out, QueryOrder{OutputAlias: alias, Direction: dir})
		if !(p.cur().kind == sqlPunct && p.cur().text == ",") {
			break
		}
		p.advance()
	}
	return out, nil
}

func (p *sqlImportParser) parseLimit() (*int, error) {
	t := p.cur()
	if t.kind != sqlNumber {
		return nil, p.errf("expected LIMIT number, got %q", t.text)
	}
	n, err := strconv.Atoi(t.text)
	if err != nil {
		return nil, p.errf("invalid LIMIT %q", t.text)
	}
	p.advance()
	if p.cur().kind == sqlPunct && p.cur().text == "," {
		return nil, p.errf("LIMIT offset is not supported")
	}
	if p.isKeyword(p.cur(), "offset") {
		return nil, p.errf("LIMIT offset is not supported")
	}
	return &n, nil
}

func (p *sqlImportParser) parseOr() (*QueryPredicate, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	if !p.isKeyword(p.cur(), "or") {
		return left, nil
	}
	children := []QueryPredicate{*left}
	for p.matchKeyword("or") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		children = append(children, *right)
	}
	return &QueryPredicate{Or: children}, nil
}

func (p *sqlImportParser) parseAnd() (*QueryPredicate, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	if !p.isKeyword(p.cur(), "and") {
		return left, nil
	}
	children := []QueryPredicate{*left}
	for p.matchKeyword("and") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		children = append(children, *right)
	}
	return &QueryPredicate{And: children}, nil
}

func (p *sqlImportParser) parseNot() (*QueryPredicate, error) {
	if p.matchKeyword("not") {
		child, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &QueryPredicate{Not: child}, nil
	}
	return p.parsePrimary()
}

func (p *sqlImportParser) parsePrimary() (*QueryPredicate, error) {
	if p.cur().kind == sqlPunct && p.cur().text == "(" {
		p.advance()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	return p.parseComparison()
}

func (p *sqlImportParser) parseComparison() (*QueryPredicate, error) {
	f, err := p.parseQualifiedField(false)
	if err != nil {
		return nil, err
	}
	if p.isKeyword(p.cur(), "is") {
		p.advance()
		not := p.matchKeyword("not")
		if err := p.expectKeyword("null"); err != nil {
			return nil, err
		}
		op := "is_null"
		if not {
			op = "is_not_null"
		}
		return &QueryPredicate{Field: &f, Operator: op}, nil
	}
	op, err := p.parseComparisonOperator()
	if err != nil {
		return nil, err
	}
	if op == "in" {
		if p.cur().kind == sqlParam {
			name := p.cur().text
			p.advance()
			p.noteParam(name)
			return &QueryPredicate{Field: &f, Operator: op, Parameter: name}, nil
		}
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		var values []any
		for {
			_, value, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			values = append(values, value)
			if p.cur().kind == sqlPunct && p.cur().text == "," {
				p.advance()
				continue
			}
			break
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return &QueryPredicate{Field: &f, Operator: op, Value: values}, nil
	}
	param, value, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	return &QueryPredicate{Field: &f, Operator: op, Parameter: param, Value: value}, nil
}

func (p *sqlImportParser) parseComparisonOperator() (string, error) {
	t := p.cur()
	if t.kind == sqlPunct {
		switch t.text {
		case "=":
			p.advance()
			return "eq", nil
		case "!=", "<>":
			p.advance()
			return "ne", nil
		case ">":
			p.advance()
			return "gt", nil
		case ">=":
			p.advance()
			return "gte", nil
		case "<":
			p.advance()
			return "lt", nil
		case "<=":
			p.advance()
			return "lte", nil
		}
	}
	if p.isKeyword(t, "like") {
		p.advance()
		return "like", nil
	}
	if p.isKeyword(t, "in") {
		p.advance()
		return "in", nil
	}
	return "", p.errf("unsupported comparison operator %q", t.text)
}

func (p *sqlImportParser) parseOrderedOperator() (string, error) {
	t := p.cur()
	if t.kind != sqlPunct {
		return "", p.errf("expected comparison operator, got %q", t.text)
	}
	switch t.text {
	case "=":
		p.advance()
		return "eq", nil
	case "!=", "<>":
		p.advance()
		return "ne", nil
	case ">":
		p.advance()
		return "gt", nil
	case ">=":
		p.advance()
		return "gte", nil
	case "<":
		p.advance()
		return "lt", nil
	case "<=":
		p.advance()
		return "lte", nil
	}
	return "", p.errf("unsupported comparison operator %q", t.text)
}

func (p *sqlImportParser) parseValue() (string, any, error) {
	t := p.cur()
	switch t.kind {
	case sqlParam:
		p.advance()
		p.noteParam(t.text)
		return t.text, nil, nil
	case sqlString:
		p.advance()
		return "", t.text, nil
	case sqlNumber:
		p.advance()
		if strings.ContainsAny(t.text, ".eE") {
			f, err := strconv.ParseFloat(t.text, 64)
			if err != nil {
				return "", nil, p.errf("invalid number %q", t.text)
			}
			return "", f, nil
		}
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return "", nil, p.errf("invalid number %q", t.text)
		}
		return "", n, nil
	case sqlIdent:
		if strings.EqualFold(t.text, "true") {
			p.advance()
			return "", true, nil
		}
		if strings.EqualFold(t.text, "false") {
			p.advance()
			return "", false, nil
		}
	}
	return "", nil, p.errf("expected a literal value or parameter, got %q", t.text)
}

func (p *sqlImportParser) noteParam(name string) {
	if !p.params[name] {
		p.params[name] = true
	}
}

// resolveASTFields binds bare (unqualified) column references to the single
// table alias when there is exactly one source; with multiple tables a bare
// column is ambiguous and rejected. Qualified references are checked against
// the known aliases.
func resolveASTFields(ast *QueryAST, aliases map[string]bool) error {
	single := ""
	if len(aliases) == 1 {
		for alias := range aliases {
			single = alias
		}
	}
	resolve := func(f *QueryField) error {
		if f == nil {
			return nil
		}
		if f.Source == "" {
			if single == "" {
				return fmt.Errorf("sql import: column %q is ambiguous; qualify it with a table alias", f.Column)
			}
			f.Source = single
		} else if !aliases[f.Source] {
			return fmt.Errorf("sql import: unknown table alias %q", f.Source)
		}
		return nil
	}
	for i := range ast.Select {
		if err := resolve(ast.Select[i].Field); err != nil {
			return err
		}
	}
	for i := range ast.GroupBy {
		if err := resolve(&ast.GroupBy[i]); err != nil {
			return err
		}
	}
	for i := range ast.Having {
		if err := resolve(&ast.Having[i].Field); err != nil {
			return err
		}
	}
	var walk func(*QueryPredicate) error
	walk = func(p *QueryPredicate) error {
		if p == nil {
			return nil
		}
		if p.Field != nil {
			return resolve(p.Field)
		}
		if p.Not != nil {
			return walk(p.Not)
		}
		for i := range p.And {
			if err := walk(&p.And[i]); err != nil {
				return err
			}
		}
		for i := range p.Or {
			if err := walk(&p.Or[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if ast.Where != nil {
		return walk(ast.Where)
	}
	return nil
}
