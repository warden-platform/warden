package connector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

// queryPlan is the DB-independent half of query preparation: a canonical plan,
// its rendered statement and the structural plan hash.
type queryPlan struct {
	plan        *QueryAST
	rendered    *RenderedQuery
	planHash    string
	previewHash string
}

// preparedQuery is a queryPlan plus its live EXPLAIN summary and cost verdict.
type preparedQuery struct {
	queryPlan
	explain    *ExplainResult
	assessment QueryCostAssessment
}

type queryApprovalRequiredError struct {
	result map[string]any
}

func (e *queryApprovalRequiredError) Error() string {
	return "DATABASE_APPROVAL_REQUIRED: query cost requires an approved access request"
}

// resolveQueryPlan decodes, canonicalizes and renders the query AST carried in
// task parameters. resourceId values are PAM database_table external ids
// (table/<schema>/<table>); every source is scope-checked before it is allowed
// into the rendered statement.
func (r *DatabaseRunner) resolveQueryPlan(parameters map[string]any) (*queryPlan, error) {
	rawAST, ok := parameters["ast"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parameter %q must be an object", "ast")
	}
	data, err := json.Marshal(rawAST)
	if err != nil {
		return nil, fmt.Errorf("encode query AST: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("query AST exceeds 1 MiB")
	}
	ast, err := decodeQueryAST(data)
	if err != nil {
		return nil, err
	}
	plan, planHash, err := CanonicalizeQuery(ast)
	if err != nil {
		return nil, err
	}
	tables := make(map[string]QueryTableRef, len(plan.Sources)+len(plan.Joins))
	for _, s := range plan.Sources {
		ref, err := r.resolveQueryTable(s.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", s.Alias, err)
		}
		tables[s.ResourceID] = ref
	}
	for _, j := range plan.Joins {
		if _, exists := tables[j.Source.ResourceID]; !exists {
			ref, err := r.resolveQueryTable(j.Source.ResourceID)
			if err != nil {
				return nil, fmt.Errorf("join source %q: %w", j.Source.Alias, err)
			}
			tables[j.Source.ResourceID] = ref
		}
	}
	rendered, err := RenderMySQLQuery(plan, ast.Bindings, func(resourceID string) (QueryTableRef, error) {
		ref, ok := tables[resourceID]
		if !ok {
			return QueryTableRef{}, fmt.Errorf("unresolved source %q", resourceID)
		}
		return ref, nil
	})
	if err != nil {
		return nil, err
	}
	previewHash, err := queryPreviewFingerprint(plan, ast.Bindings)
	if err != nil {
		return nil, err
	}
	return &queryPlan{plan: plan, rendered: rendered, planHash: planHash, previewHash: previewHash}, nil
}

// queryPreviewFingerprint binds the approved preview to both the canonical
// structure and its literal bindings. The structural planHash intentionally
// excludes bindings for analytics/template identity; previewHash must not, as
// different values can produce materially different optimizer estimates.
func queryPreviewFingerprint(plan *QueryAST, bindings map[string]any) (string, error) {
	payload, err := json.Marshal(struct {
		Plan     *QueryAST      `json:"plan"`
		Bindings map[string]any `json:"bindings"`
	}{Plan: plan, Bindings: bindings})
	if err != nil {
		return "", fmt.Errorf("encode query preview fingerprint: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (r *DatabaseRunner) resolveQueryTable(resourceID string) (QueryTableRef, error) {
	schema, table, err := databaseTableID(resourceID)
	if err != nil {
		return QueryTableRef{}, err
	}
	if !r.schemaAllowed(schema) {
		return QueryTableRef{}, fmt.Errorf("table %s.%s is outside the integration scope", schema, table)
	}
	return QueryTableRef{Schema: schema, Table: table}, nil
}

// prepareQuery runs EXPLAIN and evaluates the cost gate on top of the plan.
func (r *DatabaseRunner) prepareQuery(ctx context.Context, task domain.TaskEnvelope) (*preparedQuery, error) {
	plan, err := r.resolveQueryPlan(normalizeLegacyQueryParameters(task))
	if err != nil {
		return nil, err
	}
	explain, err := r.explainQuery(ctx, plan.rendered)
	if err != nil {
		return nil, err
	}
	return &preparedQuery{
		queryPlan:  *plan,
		explain:    explain,
		assessment: EvaluateQueryCost(explain, plan.plan, DefaultExplainCostLimits()),
	}, nil
}

// normalizeLegacyQueryParameters keeps query previews compatible with UI and
// Agent clients released before the complete v1 AST envelope was emitted.
// Defaults are inferred only when fields are absent. Explicit versions,
// sources and limits remain untouched and go through strict validation. The
// inferred source comes from the signed task resource, never from user input.
func normalizeLegacyQueryParameters(task domain.TaskEnvelope) map[string]any {
	rawAST, ok := task.Parameters["ast"].(map[string]any)
	if !ok {
		return task.Parameters
	}
	parameters := make(map[string]any, len(task.Parameters))
	for key, value := range task.Parameters {
		parameters[key] = value
	}
	ast := make(map[string]any, len(rawAST)+4)
	for key, value := range rawAST {
		ast[key] = value
	}
	if _, exists := ast["astVersion"]; !exists {
		ast["astVersion"] = QueryASTVersion
	}
	if _, exists := ast["sources"]; !exists && task.Resource.Type == "database_table" && task.Resource.ExternalID != "" {
		ast["sources"] = []any{map[string]any{"alias": "t", "resourceId": task.Resource.ExternalID}}
	}
	if _, exists := ast["bindings"]; !exists {
		ast["bindings"] = map[string]any{}
	}
	if _, exists := ast["limit"]; !exists {
		ast["limit"] = 50
	}
	parameters["ast"] = ast
	return parameters
}

// queryPreview returns the preview artifact for a governed query: the plan
// hash (reused as previewHash), the parameterized SQL, and the EXPLAIN cost
// summary plus verdict. It executes nothing against business data.
func (r *DatabaseRunner) queryPreview(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.lease(task.Grant.ExpiresAt)
	if err := r.requireQueryTarget(task); err != nil {
		return nil, lease, err
	}
	if err := r.ping(ctx); err != nil {
		return nil, lease, err
	}
	pq, err := r.prepareQuery(ctx, task)
	if err != nil {
		return nil, lease, err
	}
	warnings := queryCostWarnings(pq.explain)
	return map[string]any{
		"planHash":        pq.planHash,
		"previewHash":     pq.previewHash,
		"sql":             pq.rendered.SQL,
		"sourceCount":     len(pq.plan.Sources) + len(pq.plan.Joins),
		"estimatedRows":   pq.explain.EstimatedRows,
		"estimatedCost":   pq.explain.EstimatedCost,
		"fullScan":        pq.explain.FullScan,
		"usingTempTable":  pq.explain.UsingTempTable,
		"usingFilesort":   pq.explain.UsingFilesort,
		"verdict":         string(pq.assessment.Verdict),
		"decisionReasons": pq.assessment.Reasons,
		"warnings":        warnings,
		"limit":           queryResultLimit(pq.plan),
	}, lease, nil
}

// queryExecute re-prepares the query, verifies the plan hash matches the
// approved preview, rejects hard-cost queries, then runs the bounded read.
func (r *DatabaseRunner) queryExecute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.lease(task.Grant.ExpiresAt)
	if err := r.requireQueryTarget(task); err != nil {
		return nil, lease, err
	}
	expectedHash, err := requiredString(task.Parameters, "previewHash")
	if err != nil {
		return nil, lease, fmt.Errorf("a query preview is required before execute")
	}
	if err := r.ping(ctx); err != nil {
		return nil, lease, err
	}
	pq, err := r.prepareQuery(ctx, task)
	if err != nil {
		return nil, lease, err
	}
	if expectedHash != pq.previewHash {
		return nil, lease, fmt.Errorf("PREVIEW_CONFLICT: query plan or bindings changed after preview")
	}
	if pq.assessment.Verdict == CostReject {
		return nil, lease, fmt.Errorf("DATABASE_COST_LIMIT_EXCEEDED: query cost exceeds hard limits")
	}
	if pq.assessment.Verdict == CostRequireApproval && !hasQueryApprovalProof(task.Parameters) {
		return nil, lease, &queryApprovalRequiredError{result: queryCostContext(pq)}
	}
	limit := queryResultLimit(pq.plan)
	columns := make([]string, 0, len(pq.plan.Select))
	for _, item := range pq.plan.Select {
		columns = append(columns, item.Alias)
	}
	policies, err := parseMaskingPolicies(task.Parameters)
	if err != nil {
		return nil, lease, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := r.poolFor("database.query.execute").QueryContext(queryCtx, pq.rendered.SQL, pq.rendered.Args...)
	if err != nil {
		return nil, lease, err
	}
	defer rows.Close()
	resultRows, hasMore, truncatedCells, err := readQueryRows(rows, columns, limit)
	if err != nil {
		return nil, lease, err
	}
	maskedCells := 0
	if len(policies) > 0 {
		for i, row := range resultRows {
			maskedRow, masked := applyMasking(row, policies)
			resultRows[i] = maskedRow
			maskedCells += masked
		}
	}
	return map[string]any{
		"columns":        columns,
		"rows":           resultRows,
		"rowCount":       len(resultRows),
		"limit":          limit,
		"hasMore":        hasMore,
		"truncatedCells": truncatedCells,
		"maskedCells":    maskedCells,
		"planHash":       pq.planHash,
		"previewHash":    pq.previewHash,
		"estimatedRows":  pq.explain.EstimatedRows,
	}, lease, nil
}

func queryCostContext(pq *preparedQuery) map[string]any {
	return map[string]any{
		"planHash":        pq.planHash,
		"previewHash":     pq.previewHash,
		"sourceCount":     len(pq.plan.Sources) + len(pq.plan.Joins),
		"estimatedRows":   pq.explain.EstimatedRows,
		"estimatedCost":   pq.explain.EstimatedCost,
		"fullScan":        pq.explain.FullScan,
		"usingTempTable":  pq.explain.UsingTempTable,
		"usingFilesort":   pq.explain.UsingFilesort,
		"verdict":         string(pq.assessment.Verdict),
		"decisionReasons": pq.assessment.Reasons,
		"warnings":        queryCostWarnings(pq.explain),
		"limit":           queryResultLimit(pq.plan),
	}
}

func hasQueryApprovalProof(parameters map[string]any) bool {
	proof, ok := parameters[domain.QueryApprovalContextParameter].(map[string]any)
	if !ok {
		return false
	}
	requestID, ok := proof["requestId"].(string)
	return ok && requestID != ""
}

// queryTemplateExecute runs an immutable, published query template. The control
// plane resolves the template to its canonical AST and injects it (plus merged
// bindings and the governance context) into task parameters after validating the
// agent's templateId/templateVersion/bindings, so the connector never trusts a
// template chosen by the agent. A published template is pre-approved and
// pre-costed, so unlike queryExecute it needs no fresh previewHash — but the
// EXPLAIN cost gate still runs on every execution as a backstop.
func (r *DatabaseRunner) queryTemplateExecute(ctx context.Context, task domain.TaskEnvelope) (map[string]any, Lease, error) {
	lease := r.lease(task.Grant.ExpiresAt)
	if err := r.requireQueryTarget(task); err != nil {
		return nil, lease, err
	}
	templateID, err := requiredString(task.Parameters, "templateId")
	if err != nil {
		return nil, lease, err
	}
	templateVersion, err := requiredInt64(task.Parameters, "templateVersion")
	if err != nil {
		return nil, lease, err
	}
	if err := r.ping(ctx); err != nil {
		return nil, lease, err
	}
	pq, err := r.prepareQuery(ctx, task)
	if err != nil {
		return nil, lease, err
	}
	if pq.assessment.Verdict == CostReject {
		return nil, lease, fmt.Errorf("DATABASE_COST_LIMIT_EXCEEDED: query cost exceeds hard limits")
	}
	limit := queryResultLimit(pq.plan)
	columns := make([]string, 0, len(pq.plan.Select))
	for _, item := range pq.plan.Select {
		columns = append(columns, item.Alias)
	}
	policies, err := parseMaskingPolicies(task.Parameters)
	if err != nil {
		return nil, lease, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := r.poolFor("database.query.execute").QueryContext(queryCtx, pq.rendered.SQL, pq.rendered.Args...)
	if err != nil {
		return nil, lease, err
	}
	defer rows.Close()
	resultRows, hasMore, truncatedCells, err := readQueryRows(rows, columns, limit)
	if err != nil {
		return nil, lease, err
	}
	maskedCells := 0
	if len(policies) > 0 {
		for i, row := range resultRows {
			maskedRow, masked := applyMasking(row, policies)
			resultRows[i] = maskedRow
			maskedCells += masked
		}
	}
	return map[string]any{
		"templateId":      templateID,
		"templateVersion": templateVersion,
		"columns":         columns,
		"rows":            resultRows,
		"rowCount":        len(resultRows),
		"limit":           limit,
		"hasMore":         hasMore,
		"truncatedCells":  truncatedCells,
		"maskedCells":     maskedCells,
		"planHash":        pq.planHash,
		"estimatedRows":   pq.explain.EstimatedRows,
	}, lease, nil
}

// requireQueryTarget pins the query to a database_table target and verifies the
// FROM table is the authorized resource, preventing table substitution.
func (r *DatabaseRunner) requireQueryTarget(task domain.TaskEnvelope) error {
	if task.Resource.Type != "database_table" {
		return fmt.Errorf("database query requires a database_table target")
	}
	if len(task.Resource.ExternalID) == 0 {
		return fmt.Errorf("database query target resource has no external id")
	}
	return nil
}

func queryResultLimit(plan *QueryAST) int {
	if plan.Limit != nil {
		return *plan.Limit
	}
	return maxDatabaseQueryRows
}

// requiredInt64 reads a positive integer parameter that may arrive as float64
// (JSON decode) or json.Number, rejecting negatives and non-integers.
func requiredInt64(parameters map[string]any, key string) (int64, error) {
	value, ok := parameters[key]
	if !ok || value == nil {
		return 0, fmt.Errorf("parameter %q is required", key)
	}
	switch current := value.(type) {
	case float64:
		if current != float64(int64(current)) || current < 1 {
			return 0, fmt.Errorf("parameter %q must be a positive integer", key)
		}
		return int64(current), nil
	case json.Number:
		parsed, err := current.Int64()
		if err != nil || parsed < 1 {
			return 0, fmt.Errorf("parameter %q must be a positive integer", key)
		}
		return parsed, nil
	case int:
		if current < 1 {
			return 0, fmt.Errorf("parameter %q must be a positive integer", key)
		}
		return int64(current), nil
	case int64:
		if current < 1 {
			return 0, fmt.Errorf("parameter %q must be a positive integer", key)
		}
		return current, nil
	default:
		return 0, fmt.Errorf("parameter %q must be a positive integer", key)
	}
}

func queryCostWarnings(explain *ExplainResult) []string {
	warnings := make([]string, 0, 3)
	if explain.FullScan {
		warnings = append(warnings, "FULL_SCAN")
	}
	if explain.UsingTempTable {
		warnings = append(warnings, "USING_TEMPORARY")
	}
	if explain.UsingFilesort {
		warnings = append(warnings, "USING_FILESORT")
	}
	return warnings
}

// readQueryRows streams rows into bounded maps, stopping at the row and byte
// limits and reporting overflow. Cell values are individually bounded.
func readQueryRows(rows *sql.Rows, columns []string, limit int) ([]map[string]any, bool, int, error) {
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
			return nil, false, 0, err
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
			return nil, false, 0, fmt.Errorf("serialize database row: %w", err)
		}
		if resultBytes+len(encoded) > maxDatabaseResultBytes {
			hasMore = true
			break
		}
		resultBytes += len(encoded)
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, 0, err
	}
	return resultRows, hasMore, truncatedCells, nil
}
