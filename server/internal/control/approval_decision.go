package control

import (
	"context"
	"errors"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

type approvalDecisionError struct {
	Status  int
	Code    string
	Message string
}

func (e *approvalDecisionError) Error() string { return e.Code }

func decisionError(status int, code, message string) error {
	return &approvalDecisionError{Status: status, Code: code, Message: message}
}

// decideApproval is the single transaction boundary for Web and DingTalk
// decisions. Callers must authenticate the actor and verify warden:approve before
// entering this method.
func (h *Handler) decideApproval(ctx context.Context, id string, approve bool, actor *AuthenticatedIdentity) (*domain.AccessRequest, error) {
	if h.pool == nil {
		return nil, decisionError(503, "DATABASE_UNAVAILABLE", "数据库不可用")
	}
	if actor == nil || actor.Principal == nil || !actor.HasScope("warden:approve") {
		return nil, decisionError(403, "FORBIDDEN", "当前身份没有审批权限")
	}
	req, err := h.requests.GetByID(ctx, id)
	if err != nil || req == nil {
		return nil, decisionError(404, "NOT_FOUND", "审批请求不存在")
	}
	if req.Status != domain.RequestPending {
		return nil, decisionError(409, "ALREADY_PROCESSED", "该请求已被处理")
	}
	if approve && req.ActingProjectID != "" {
		projectID, scopeErr := h.resolveResourceActingProject(ctx, req.ResourceID, req.ActingProjectID)
		if scopeErr != nil || projectID != req.ActingProjectID {
			return nil, decisionError(409, "RESOURCE_PROJECT_SCOPE_CHANGED", "资源已不再对该执行项目开放，请重新提交")
		}
		if accessErr := h.principalProjectAccess(ctx, projectID, req.PrincipalID, true); accessErr != nil {
			return nil, decisionError(409, "PROJECT_MEMBERSHIP_CHANGED", "请求身份已失去该项目的执行权限，请重新提交")
		}
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, decisionError(500, "DB_ERROR", "开始审批事务失败")
	}
	defer tx.Rollback(ctx)
	// Every decision path locks the request first. This keeps approve/reject
	// lock ordering identical and prevents a cross-channel race from issuing a
	// grant while another transaction rejects the same request.
	var locked int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM access_requests WHERE id=$1 AND status='PENDING' FOR UPDATE`, id).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, decisionError(409, "ALREADY_PROCESSED", "该请求已被其他审批人处理")
		}
		return nil, decisionError(500, "DB_ERROR", "锁定审批请求失败")
	}

	if approve {
		exec, err := h.executions.GetByID(ctx, req.ExecutionID)
		if err != nil || exec == nil || exec.Status != domain.ExecWaitingApproval {
			return nil, decisionError(409, "EXECUTION_NOT_WAITING", "关联执行已不存在或不再等待审批")
		}
		action, err := h.actions.GetByName(ctx, actionNameFromID(ctx, h.pool, req.ActionID))
		if err != nil || action == nil {
			return nil, decisionError(500, "ACTION_NOT_FOUND", "关联动作不存在")
		}
		if exec.ExecutorType == domain.ExecutorControlPlane {
			// Control-plane credential actions run inline after approval: no
			// Grant is created and the plaintext is recovered only from the
			// AES-GCM pending_secret held on the execution.
			if err := h.requests.UpdateStatusTx(ctx, tx, id, string(domain.RequestPending), string(domain.RequestApproved), actor.Principal.ID, timePtr(time.Now())); err != nil {
				return nil, decisionError(409, "ALREADY_PROCESSED", "该请求已被其他审批人处理")
			}
			res, resErr := h.resources.GetByID(ctx, req.ResourceID)
			if resErr != nil || res == nil {
				return nil, decisionError(500, "RESOURCE_NOT_FOUND", "关联资源不存在")
			}
			var secretValue string
			if len(exec.PendingSecret) > 0 {
				decrypted, decryptErr := h.credentialCipher.Decrypt(exec.PendingSecret)
				if decryptErr != nil {
					return nil, decisionError(500, "CREDENTIAL_DECRYPT_FAILED", "凭据解密失败")
				}
				secretValue = string(decrypted)
			}
			result, execErr := h.executeCredentialAction(ctx, tx, exec, action, res, secretValue)
			auditAction, auditSummary := "execution.completed", "execution.completed"
			var errorCode, errorMessage string
			if execErr != nil {
				exec.Status = domain.ExecFailed
				exec.ErrorCode = "CREDENTIAL_ACTION_FAILED"
				exec.ErrorMessage = domain.SanitizeErrorMessage(execErr.Error())
				if errors.Is(execErr, context.DeadlineExceeded) {
					exec.Status = domain.ExecUnknown
					exec.ErrorCode = "ACTION_TIMEOUT"
					exec.ErrorMessage = "凭据操作超时，结果未知；禁止自动重试"
				}
				auditAction, auditSummary = "execution.failed", "execution.failed"
				errorCode, errorMessage = exec.ErrorCode, exec.ErrorMessage
			} else {
				exec.Status = domain.ExecSucceeded
				exec.Result = result
			}
			changed, completeErr := h.executions.CompleteControlPlaneTx(ctx, tx, exec.ID, domain.ExecWaitingApproval, exec.Status, exec.Result, errorCode, errorMessage)
			if completeErr != nil {
				return nil, decisionError(500, "EXECUTION_TRANSACTION_FAILED", "保存凭据执行结果失败")
			}
			if !changed {
				return nil, decisionError(409, "EXECUTION_NOT_WAITING", "执行状态已变化")
			}
			for _, event := range []*domain.AuditEvent{
				executionAuditEvent("access_request.approved", actor, nil, nil, exec.ID, exec.TraceID),
				executionAuditEvent("execution.queued", actor, nil, nil, exec.ID, exec.TraceID),
			} {
				event.ActingProjectID = req.ActingProjectID
				attachExecutionAudit(event, exec)
				event.TargetID = req.ID
				event.ResourceID = req.ResourceID
				event.DecisionID = req.DecisionID
				event.Metadata["access_request_id"] = req.ID
				if err := h.audit.CreateTx(ctx, tx, event); err != nil {
					return nil, decisionError(500, "AUDIT_WRITE_FAILED", "审批审计写入失败")
				}
			}
			auditMeta := executionResultAuditMetadata(exec.Result)
			auditMeta["outcome"] = exec.Status
			auditMeta["action"] = action.Name
			completedEvent := executionAuditEvent(auditAction, actor, nil, nil, exec.ID, exec.TraceID)
			completedEvent.ActingProjectID = req.ActingProjectID
			attachExecutionAudit(completedEvent, exec)
			for key, value := range auditMeta {
				completedEvent.Metadata[key] = value
			}
			completedEvent.Summary = auditSummary
			completedEvent.TargetID = req.ID
			completedEvent.ResourceID = req.ResourceID
			completedEvent.DecisionID = req.DecisionID
			completedEvent.Metadata["access_request_id"] = req.ID
			if err := h.audit.CreateTx(ctx, tx, completedEvent); err != nil {
				return nil, decisionError(500, "AUDIT_WRITE_FAILED", "审批审计写入失败")
			}
		} else {
			// 审批通过时重新读取不可变的策略决策，恢复创建审批单时 Rego/OPA
			// 给出的硬上限；不能因为异步审批而丢失这层约束。
			policyMaxDuration, capErr := h.policyMaxDurationSeconds(ctx, req.DecisionID)
			if capErr != nil {
				return nil, decisionError(500, "POLICY_DECISION_READ_FAILED", "读取策略决策失败")
			}
			ttlSeconds := effectiveGrantTTLSeconds(action.TimeoutSeconds, policyMaxDuration)
			expiresAt := time.Now().Add(time.Duration(ttlSeconds) * time.Second)
			grant := &domain.AccessGrant{
				RequestID: req.ID, PrincipalID: req.PrincipalID,
				ActingProjectID: req.ActingProjectID,
				ResourceScope:   map[string]any{"resource_id": req.ResourceID, "acting_project_id": req.ActingProjectID},
				ActionScope:     map[string]any{"action": action.Name, "version": action.Version},
				ParameterHash:   req.ParameterHash,
				ScopeHash:       domain.HashScope(req.PrincipalID, req.ResourceID, req.ActionID, req.ParameterHash, req.ActingProjectID),
				MaxUses:         1, TTLSeconds: ttlSeconds, ExpiresAt: expiresAt, Status: domain.GrantActive,
			}
			if err := h.grants.CreateTx(ctx, tx, grant); err != nil {
				return nil, decisionError(500, "GRANT_CREATE_FAILED", "创建授权失败")
			}
			if err := h.executions.AuthorizeTx(ctx, tx, exec.ID, grant.ID, expiresAt); err != nil {
				return nil, decisionError(409, "EXECUTION_NOT_WAITING", "执行状态已变化")
			}
			if err := h.requests.UpdateStatusTx(ctx, tx, id, string(domain.RequestPending), string(domain.RequestApproved), actor.Principal.ID, timePtr(time.Now())); err != nil {
				return nil, decisionError(409, "ALREADY_PROCESSED", "该请求已被其他审批人处理")
			}
			for _, event := range []*domain.AuditEvent{
				executionAuditEvent("access_request.approved", actor, nil, nil, exec.ID, exec.TraceID),
				executionAuditEvent("grant.issued", actor, nil, nil, exec.ID, exec.TraceID),
				executionAuditEvent("execution.queued", actor, nil, nil, exec.ID, exec.TraceID),
			} {
				event.ActingProjectID = req.ActingProjectID
				attachExecutionAudit(event, exec)
				event.TargetID = req.ID
				event.ResourceID = req.ResourceID
				event.DecisionID = req.DecisionID
				event.GrantID = grant.ID
				event.Metadata["access_request_id"] = req.ID
				if err := h.audit.CreateTx(ctx, tx, event); err != nil {
					return nil, decisionError(500, "AUDIT_WRITE_FAILED", "审批审计写入失败")
				}
			}
		}
	} else {
		if err := h.requests.UpdateStatusTx(ctx, tx, id, string(domain.RequestPending), string(domain.RequestRejected), actor.Principal.ID, timePtr(time.Now())); err != nil {
			return nil, decisionError(409, "ALREADY_PROCESSED", "该请求已被其他审批人处理")
		}
		if req.ExecutionID != "" {
			changed, err := h.executions.TransitionStatusTx(ctx, tx, req.ExecutionID, domain.ExecWaitingApproval, domain.ExecFailed, "APPROVAL_REJECTED", "审批被拒绝")
			if err != nil || !changed {
				return nil, decisionError(409, "EXECUTION_NOT_WAITING", "执行状态已变化")
			}
		}
		rejectedAudit := executionAuditEvent("access_request.rejected", actor, nil, nil, req.ExecutionID, "")
		attachRequestAudit(rejectedAudit, req)
		rejectedAudit.ActingProjectID = req.ActingProjectID
		rejectedAudit.TargetID, rejectedAudit.ResourceID, rejectedAudit.DecisionID = req.ID, req.ResourceID, req.DecisionID
		rejectedAudit.Metadata["access_request_id"] = req.ID
		if err := h.audit.CreateTx(ctx, tx, rejectedAudit); err != nil {
			return nil, decisionError(500, "AUDIT_WRITE_FAILED", "审计写入失败")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, decisionError(500, "APPROVAL_TRANSACTION_FAILED", "提交审批事务失败")
	}
	return req, nil
}

// Keep HTTP error mapping centralized without exposing internal database
// errors to either the Web client or DingTalk callback response.
func approvalDecisionHTTPError(err error) (int, string, string) {
	var decisionErr *approvalDecisionError
	if errors.As(err, &decisionErr) {
		return decisionErr.Status, decisionErr.Code, decisionErr.Message
	}
	return 500, "APPROVAL_FAILED", "审批处理失败"
}

// effectiveGrantTTLSeconds applies the policy decision's hard upper bound to
// the action timeout. A non-positive policy cap means the decision carries no
// bound (legacy rows); the cap must never extend the action's timeout.
func effectiveGrantTTLSeconds(actionTimeoutSeconds, policyMaxDurationSeconds int) int {
	ttl := actionTimeoutSeconds
	if policyMaxDurationSeconds > 0 && policyMaxDurationSeconds < ttl {
		ttl = policyMaxDurationSeconds
	}
	return ttl
}

// policyMaxDurationSeconds reads the immutable policy decision bound attached
// to an approval request. A missing decision yields 0 (legacy data); a database
// error fails closed so no grant is issued without its bound.
func (h *Handler) policyMaxDurationSeconds(ctx context.Context, decisionID string) (int, error) {
	if h.policyDecisions == nil || decisionID == "" {
		return 0, nil
	}
	outputs, err := h.policyDecisions.OutputsByIDs(ctx, []string{decisionID})
	if err != nil {
		return 0, err
	}
	return policyMaxDurationFromOutput(outputs[decisionID]), nil
}

func policyMaxDurationFromOutput(output map[string]any) int {
	if output == nil {
		return 0
	}
	switch value := output["max_duration_seconds"].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}
