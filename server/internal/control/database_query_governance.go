package control

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

const databaseQueryPreviewValidity = 30 * time.Minute

const (
	databaseCostAllow           = "allow"
	databaseCostRequireApproval = "require_approval"
	databaseCostReject          = "reject"
)

type databaseQueryPreviewGate struct {
	ExecutionID string
	PreviewHash string
	Verdict     string
	Assessment  map[string]any
}

// prepareDatabaseQueryScope fills the legacy-safe AST envelope and verifies
// that the FROM table is the execution resource while every JOIN table is an
// ACTIVE resource in the same integration. The policy decision is anchored on
// the FROM resource; allowing it to be substituted would make a multi-table UI
// an authorization bypass rather than a query feature.
func (h *Handler) prepareDatabaseQueryScope(ctx context.Context, resource *domain.Resource, parameters map[string]any) error {
	rawAST, ok := parameters["ast"].(map[string]any)
	if !ok {
		return fmt.Errorf("parameter %q must be an object", "ast")
	}
	ast := make(map[string]any, len(rawAST)+5)
	for key, value := range rawAST {
		ast[key] = value
	}
	if _, exists := ast["astVersion"]; !exists {
		ast["astVersion"] = 1
	}
	if _, exists := ast["sources"]; !exists {
		ast["sources"] = []any{map[string]any{"alias": "t", "resourceId": resource.ExternalID}}
	}
	if _, exists := ast["bindings"]; !exists {
		ast["bindings"] = map[string]any{}
	}
	if _, exists := ast["orderBy"]; !exists {
		ast["orderBy"] = []any{}
	}
	if _, exists := ast["limit"]; !exists {
		ast["limit"] = 50
	}
	parameters["ast"] = ast
	externalIDs, err := databaseQueryExternalIDs(resource, ast)
	if err != nil {
		return err
	}
	active, err := h.resources.ActiveDatabaseTableExternalIDs(ctx, resource.IntegrationID, externalIDs)
	if err != nil {
		return fmt.Errorf("validate complex query tables: %w", err)
	}
	for _, externalID := range externalIDs {
		if !active[externalID] {
			return fmt.Errorf("query table %q is not an ACTIVE resource in the selected database integration", externalID)
		}
	}
	return nil
}

func databaseQueryExternalIDs(resource *domain.Resource, ast map[string]any) ([]string, error) {
	sources, ok := ast["sources"].([]any)
	if !ok || len(sources) != 1 {
		return nil, fmt.Errorf("complex query must contain exactly one FROM source; additional tables must use JOIN")
	}
	base, ok := sources[0].(map[string]any)
	if !ok || strings.TrimSpace(astString(base["resourceId"])) != resource.ExternalID {
		return nil, fmt.Errorf("complex query FROM source must be the selected table resource")
	}
	externalIDs := []string{resource.ExternalID}
	seen := map[string]bool{resource.ExternalID: true}
	if rawJoins, exists := ast["joins"]; exists {
		joins, ok := rawJoins.([]any)
		if !ok || len(joins) > 8 {
			return nil, fmt.Errorf("complex query joins must be an array with at most 8 entries")
		}
		for _, rawJoin := range joins {
			join, ok := rawJoin.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("complex query join must be an object")
			}
			source, ok := join["source"].(map[string]any)
			externalID := strings.TrimSpace(astString(source["resourceId"]))
			if !ok || externalID == "" || seen[externalID] {
				return nil, fmt.Errorf("complex query JOIN tables must be non-empty and unique")
			}
			seen[externalID] = true
			externalIDs = append(externalIDs, externalID)
		}
	}
	return externalIDs, nil
}

// escalateRuntimeDatabaseQueryApproval handles the narrow race where database
// statistics change after preview and the Connector's second EXPLAIN raises an
// allow verdict to require_approval. The dispatched read has not run yet, so
// the same execution can safely return to WAITING_APPROVAL with a fresh,
// single-use approval request.
func (h *Handler) escalateRuntimeDatabaseQueryApproval(ctx context.Context, exec *domain.Execution, dispatchToken string, result map[string]any) (*domain.AccessRequest, bool, error) {
	assessment := databaseQueryApprovalContext(result)
	assessment["verdict"] = databaseCostRequireApproval
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE executions
		SET status='WAITING_APPROVAL', result=$3, error_code=NULL, error_message=NULL,
			grant_id=NULL, expires_at=NULL, dispatch_token=NULL, dispatch_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND dispatch_token=$2::uuid AND status='DISPATCHED'
			AND tenant_id=$4 AND dispatch_expires_at >= now()`, exec.ID, dispatchToken, assessment, domain.TenantIDFromContext(ctx))
	if err != nil || command.RowsAffected() != 1 {
		return nil, command.RowsAffected() == 1, err
	}
	reason := strings.TrimSpace(exec.Reason)
	if reason == "" {
		reason = "执行前成本复检升级为需审批"
	}
	request := &domain.AccessRequest{
		ExecutionID: exec.ID, PrincipalID: exec.PrincipalID, ResourceID: exec.ResourceID,
		ActionID: exec.ActionID, ActingProjectID: exec.ActingProjectID,
		ExternalContext:   exec.ExternalContext,
		DelegationTokenID: exec.DelegationTokenID, OnBehalfOfPrincipalID: exec.OnBehalfOfPrincipalID,
		AccountablePrincipalID: exec.AccountablePrincipalID,
		Parameters:             exec.Parameters, ParameterHash: exec.ParameterHash,
		Reason: reason, Status: domain.RequestPending, DecisionID: exec.DecisionID,
		ExpiresAt: timePtr(time.Now().Add(30 * time.Minute)),
	}
	if err := h.requests.CreateTx(ctx, tx, request); err != nil {
		return nil, false, err
	}
	if exec.DecisionID != "" {
		if _, err := tx.Exec(ctx, `UPDATE policy_decisions
			SET result='REQUIRE_APPROVAL', opa_output=opa_output || jsonb_build_object(
				'database_query_cost_verdict','require_approval',
				'database_query_cost_assessment',$2::jsonb,
				'database_query_runtime_cost_floor',true)
			WHERE id=$1 AND tenant_id=$3`, exec.DecisionID, assessment, domain.TenantIDFromContext(ctx)); err != nil {
			return nil, false, err
		}
	}
	audit := &domain.AuditEvent{
		Action: "access_request.created", ActorID: exec.PrincipalID, ResourceID: exec.ResourceID,
		ActingProjectID: exec.ActingProjectID,
		ExecutionID:     exec.ID, DecisionID: exec.DecisionID, ConnectorID: exec.ConnectorID,
		TraceID: exec.TraceID, TargetID: request.ID, Summary: "access_request.created",
		Metadata: map[string]any{"access_request_id": request.ID, "trigger": "runtime_query_cost_recheck"},
	}
	attachExecutionAudit(audit, exec)
	if err := h.audit.CreateTx(ctx, tx, audit); err != nil {
		return nil, false, err
	}
	if err := h.outbox.PublishTx(ctx, tx, "access_request.submitted.v1", "execution", exec.ID, map[string]any{
		"execution_id": exec.ID, "status": domain.ExecWaitingApproval, "trace_id": exec.TraceID,
	}); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return request, true, nil
}

// loadDatabaseQueryPreviewGate turns a Connector-authenticated preview result
// into a control-plane authorization input. A hash supplied by the caller is
// never trusted by itself: it must resolve to a recent successful preview by
// the same principal against the same resource and acting project.
func (h *Handler) loadDatabaseQueryPreviewGate(ctx context.Context, principalID, resourceID, actingProjectID string, parameters map[string]any) (*databaseQueryPreviewGate, error) {
	if err := validateDatabaseQueryClientParameters(parameters); err != nil {
		return nil, err
	}
	previewHash, ok := parameters["previewHash"].(string)
	previewHash = strings.TrimSpace(previewHash)
	decoded, decodeErr := hex.DecodeString(previewHash)
	if !ok || decodeErr != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("a valid query previewHash is required")
	}
	preview, err := h.executions.GetRecentDatabaseQueryPreview(ctx, principalID, resourceID, actingProjectID, previewHash, databaseQueryPreviewValidity)
	if err != nil {
		return nil, fmt.Errorf("load query preview: %w", err)
	}
	if preview == nil {
		return nil, fmt.Errorf("query preview is missing, expired, or belongs to another principal/resource/project")
	}
	switch preview.Verdict {
	case databaseCostAllow, databaseCostRequireApproval, databaseCostReject:
	default:
		return nil, fmt.Errorf("query preview returned an unknown cost verdict %q", preview.Verdict)
	}
	return &databaseQueryPreviewGate{
		ExecutionID: preview.ExecutionID,
		PreviewHash: preview.PreviewHash,
		Verdict:     preview.Verdict,
		Assessment:  databaseQueryApprovalContext(preview.Result),
	}, nil
}

func validateDatabaseQueryClientParameters(parameters map[string]any) error {
	if _, exists := parameters[domain.QueryApprovalContextParameter]; exists {
		return fmt.Errorf("parameter %q is reserved for the control plane", domain.QueryApprovalContextParameter)
	}
	return nil
}

func databaseQueryApprovalContext(result map[string]any) map[string]any {
	context := make(map[string]any)
	for _, key := range []string{
		"verdict", "previewHash", "planHash", "estimatedRows", "estimatedCost", "fullScan", "usingTempTable",
		"usingFilesort", "limit", "sourceCount", "decisionReasons", "warnings",
	} {
		if value, ok := result[key]; ok {
			context[key] = value
		}
	}
	return context
}

// enforceDatabaseQueryCostFloor makes the preview verdict an immutable policy
// floor. Normal policy can still require approval for an otherwise cheap query,
// but no matrix ALLOW rule may downgrade a cost-driven approval or rejection.
func enforceDatabaseQueryCostFloor(current domain.DecisionResult, verdict string) domain.DecisionResult {
	if current == domain.DecisionDeny {
		return current
	}
	switch verdict {
	case databaseCostReject:
		return domain.DecisionDeny
	case databaseCostRequireApproval:
		return domain.DecisionRequireApproval
	default:
		return current
	}
}
