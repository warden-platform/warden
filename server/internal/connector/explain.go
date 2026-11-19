package connector

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// ExplainResult is the cost summary extracted from EXPLAIN FORMAT=JSON. It feeds
// the cost gate (§12.2) and the preview artifact (estimatedRows / estimatedCost).
type ExplainResult struct {
	EstimatedRows  float64
	EstimatedCost  float64
	FullScan       bool
	UsingTempTable bool
	UsingFilesort  bool
	AccessTypes    []string
}

// CostVerdict is the outcome of the cost gate.
type CostVerdict string

const (
	CostAllow           CostVerdict = "allow"
	CostRequireApproval CostVerdict = "require_approval"
	CostReject          CostVerdict = "reject"
)

// ExplainCostLimits is the policy thresholds for the cost gate. Production
// defaults are conservative; a deployment may tighten them further.
type ExplainCostLimits struct {
	HardRows           float64
	SoftRows           float64
	HardCost           float64
	SoftCost           float64
	StructuralRows     float64
	StructuralCost     float64
	BoundedSimpleRows  float64
	BoundedSimpleCost  float64
	BoundedSimpleLimit int
	AllowFullScan      bool
}

// QueryCostAssessment records both the enforcement outcome and stable reason
// codes. The reasons are returned by previews and persisted by the control
// plane, so an approval request explains exactly which signals escalated it.
type QueryCostAssessment struct {
	Verdict CostVerdict
	Reasons []string
}

// DefaultExplainCostLimits returns the production-default thresholds.
func DefaultExplainCostLimits() ExplainCostLimits {
	return ExplainCostLimits{
		HardRows:           100000,
		SoftRows:           10000,
		HardCost:           5000,
		SoftCost:           500,
		StructuralRows:     1000,
		StructuralCost:     250,
		BoundedSimpleRows:  10000,
		BoundedSimpleCost:  1000,
		BoundedSimpleLimit: 100,
		AllowFullScan:      false,
	}
}

// EvaluateQueryCost combines optimizer estimates with the shape and output
// bound of the canonical query. LIMIT reduces result and transport risk, but it
// only reduces scan work for a simple single-table query without a blocking
// join, aggregate, grouping, temporary table or sort. That narrow shape gets a
// bounded exception below the soft thresholds; hard thresholds are never
// bypassed by LIMIT.
func EvaluateQueryCost(res *ExplainResult, plan *QueryAST, limits ExplainCostLimits) QueryCostAssessment {
	reasons := make([]string, 0, 5)
	if res.EstimatedRows > limits.HardRows || res.EstimatedCost > limits.HardCost {
		if res.EstimatedRows > limits.HardRows {
			reasons = append(reasons, "HARD_ROWS_EXCEEDED")
		}
		if res.EstimatedCost > limits.HardCost {
			reasons = append(reasons, "HARD_COST_EXCEEDED")
		}
		return QueryCostAssessment{Verdict: CostReject, Reasons: reasons}
	}

	if isBoundedSimpleQuery(res, plan, limits) {
		return QueryCostAssessment{Verdict: CostAllow}
	}
	if res.EstimatedRows > limits.SoftRows {
		reasons = append(reasons, "SOFT_ROWS_EXCEEDED")
	}
	if res.EstimatedCost > limits.SoftCost {
		reasons = append(reasons, "SOFT_COST_EXCEEDED")
	}
	// Optimizer flags are not dangerous by themselves on tiny inputs. Escalate
	// them only when the work is material or the optimizer failed to provide a
	// usable row estimate; this avoids approving harmless sorts/full scans of
	// small tables while remaining fail-closed for unknown plans.
	materialStructure := res.EstimatedRows <= 0 || res.EstimatedRows > limits.StructuralRows || res.EstimatedCost > limits.StructuralCost
	if materialStructure {
		if res.FullScan && !limits.AllowFullScan {
			reasons = append(reasons, "FULL_SCAN")
		}
		if res.UsingTempTable {
			reasons = append(reasons, "USING_TEMPORARY")
		}
		if res.UsingFilesort {
			reasons = append(reasons, "USING_FILESORT")
		}
	}
	if len(reasons) > 0 {
		return QueryCostAssessment{Verdict: CostRequireApproval, Reasons: reasons}
	}
	return QueryCostAssessment{Verdict: CostAllow}
}

// EvaluateExplainCost keeps callers without an AST conservative. Production
// query execution uses EvaluateQueryCost with the canonical plan.
func EvaluateExplainCost(res *ExplainResult, limits ExplainCostLimits) CostVerdict {
	return EvaluateQueryCost(res, nil, limits).Verdict
}

func isBoundedSimpleQuery(res *ExplainResult, plan *QueryAST, limits ExplainCostLimits) bool {
	if plan == nil || plan.Limit == nil || *plan.Limit > limits.BoundedSimpleLimit || *plan.Limit < 1 {
		return false
	}
	if len(plan.Sources) != 1 || len(plan.Joins) != 0 || len(plan.GroupBy) != 0 || len(plan.Having) != 0 || len(plan.OrderBy) != 0 {
		return false
	}
	for _, item := range plan.Select {
		if item.Aggregate != "" || item.Distinct {
			return false
		}
	}
	if res.UsingTempTable || res.UsingFilesort || res.EstimatedRows <= 0 ||
		res.EstimatedRows > limits.BoundedSimpleRows || res.EstimatedCost > limits.BoundedSimpleCost {
		return false
	}
	return true
}

// explainQuery runs EXPLAIN FORMAT=JSON for a rendered query. The rendered SQL
// is interpolated to literals first: EXPLAIN over a server-side prepared
// statement is unreliable across MySQL versions, and the values are validated
// scalars from bindings, never raw SQL.
func (r *DatabaseRunner) explainQuery(ctx context.Context, rendered *RenderedQuery) (*ExplainResult, error) {
	explainSQL := "EXPLAIN FORMAT=JSON " + interpolateMySQL(rendered.SQL, rendered.Args)
	var out string
	if err := r.db.QueryRowContext(ctx, explainSQL).Scan(&out); err != nil {
		return nil, err
	}
	return parseExplainJSON([]byte(out))
}

// parseExplainJSON extracts the cost signals that matter for the gate. It walks
// the nested tree rather than pinning to a fixed shape, so it tolerates the
// MySQL 5.7 / 8.0 layout differences.
func parseExplainJSON(data []byte) (*ExplainResult, error) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	res := &ExplainResult{}
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if cost, ok := parseExplainFloat(value["query_cost"]); ok && cost > res.EstimatedCost {
				res.EstimatedCost = cost
			}
			if access, ok := value["access_type"].(string); ok && access != "" {
				res.AccessTypes = append(res.AccessTypes, access)
				if access == "ALL" {
					res.FullScan = true
				}
			}
			if rows, ok := parseExplainFloat(value["rows_examined_per_scan"]); ok && rows > res.EstimatedRows {
				res.EstimatedRows = rows
			}
			if flag, ok := value["using_filesort"].(bool); ok && flag {
				res.UsingFilesort = true
			}
			if flag, ok := value["using_temporary"].(bool); ok && flag {
				res.UsingTempTable = true
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(root)
	return res, nil
}

func parseExplainFloat(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case json.Number:
		f, err := number.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(number, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// interpolateMySQL substitutes `?` placeholders with validated scalar literals,
// skipping placeholders inside backtick identifiers and single-quoted strings
// so an unusual column name can never be confused for a binding slot.
func interpolateMySQL(sql string, args []any) string {
	var b strings.Builder
	argIndex := 0
	inBacktick := false
	inString := false
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		switch {
		case inBacktick:
			b.WriteByte(ch)
			if ch == '`' {
				if i+1 < len(sql) && sql[i+1] == '`' {
					b.WriteByte('`')
					i++
					continue
				}
				inBacktick = false
			}
		case inString:
			b.WriteByte(ch)
			if ch == '\\' && i+1 < len(sql) {
				b.WriteByte(sql[i+1])
				i++
				continue
			}
			if ch == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				inString = false
			}
		default:
			switch ch {
			case '`':
				inBacktick = true
				b.WriteByte(ch)
			case '\'':
				inString = true
				b.WriteByte(ch)
			case '?':
				if argIndex < len(args) {
					b.WriteString(renderMySQLLiteral(args[argIndex]))
					argIndex++
				} else {
					b.WriteByte(ch)
				}
			default:
				b.WriteByte(ch)
			}
		}
	}
	return b.String()
}

func renderMySQLLiteral(value any) string {
	switch number := value.(type) {
	case nil:
		return "NULL"
	case bool:
		if number {
			return "TRUE"
		}
		return "FALSE"
	case string:
		return "'" + escapeMySQLString(number) + "'"
	case int:
		return strconv.Itoa(number)
	case int32:
		return strconv.FormatInt(int64(number), 10)
	case int64:
		return strconv.FormatInt(number, 10)
	case float64:
		return strconv.FormatFloat(number, 'g', -1, 64)
	case json.Number:
		return number.String()
	default:
		return "'" + escapeMySQLString(strings.TrimSpace(toJSONString(value))) + "'"
	}
}

func toJSONString(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func escapeMySQLString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "'", "\\'")
	return value
}
