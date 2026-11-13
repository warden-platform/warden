package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

const (
	defaultDelegationTokenTTL = 5 * time.Minute
	maxDelegationTokenTTL     = 15 * time.Minute
)

type externalBindingRequest struct {
	Source            string                      `json:"source"`
	WorkspaceID       string                      `json:"workspaceId"`
	ExternalProjectID string                      `json:"externalProjectId"`
	IdentityType      domain.ExternalIdentityType `json:"identityType"`
	ExternalID        string                      `json:"externalId"`
	PrincipalID       string                      `json:"principalId"`
	ProjectID         string                      `json:"projectId"`
}

func validateExternalBindingInput(input externalBindingRequest) error {
	input.Source = strings.TrimSpace(input.Source)
	if !externalContextSourcePattern.MatchString(input.Source) {
		return errors.New("source 必须是小写平台标识")
	}
	for name, value := range map[string]string{
		"workspaceId": input.WorkspaceID, "externalId": input.ExternalID,
	} {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 255 || !externalContextIDPattern.MatchString(value) {
			return fmt.Errorf("%s 格式非法或长度超过 255", name)
		}
	}
	if input.ExternalProjectID != "" && (len(input.ExternalProjectID) > 255 || !externalContextIDPattern.MatchString(input.ExternalProjectID)) {
		return errors.New("externalProjectId 格式非法或长度超过 255")
	}
	if input.IdentityType != domain.ExternalIdentityAgent && input.IdentityType != domain.ExternalIdentityUser {
		return errors.New("identityType 仅支持 AGENT 或 USER")
	}
	if input.PrincipalID == "" {
		return errors.New("principalId 为必填")
	}
	if input.IdentityType == domain.ExternalIdentityUser && input.ProjectID != "" {
		return errors.New("USER Binding 不能绑定 PAM projectId")
	}
	if input.IdentityType == domain.ExternalIdentityAgent && input.ProjectID == "" {
		return errors.New("AGENT Binding 必须绑定 PAM projectId")
	}
	if input.IdentityType == domain.ExternalIdentityAgent && input.ExternalProjectID == "" {
		return errors.New("AGENT Binding 必须绑定 externalProjectId")
	}
	return nil
}

func (h *Handler) CreateExternalBinding(w http.ResponseWriter, r *http.Request) {
	if h.externalIdentities == nil || h.principals == nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "外部身份 Binding 尚未初始化")
		return
	}
	var input externalBindingRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_EXTERNAL_BINDING", "请求体解析失败")
		return
	}
	input.Source = strings.TrimSpace(input.Source)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ExternalProjectID = strings.TrimSpace(input.ExternalProjectID)
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if err := validateExternalBindingInput(input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_EXTERNAL_BINDING", err.Error())
		return
	}
	actor := identityFromContext(r.Context())
	if actor == nil || actor.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "缺少认证身份")
		return
	}
	target, err := h.principals.GetByID(r.Context(), input.PrincipalID)
	if err != nil || target == nil || target.TenantID != actor.TenantID {
		jsonError(w, http.StatusNotFound, "PRINCIPAL_NOT_FOUND", "Principal 不存在")
		return
	}
	if input.IdentityType == domain.ExternalIdentityAgent {
		if target.Kind != domain.PrincipalKindAgentInstance && target.Kind != domain.PrincipalKindWorkload {
			jsonError(w, http.StatusBadRequest, "INVALID_BINDING_PRINCIPAL", "AGENT Binding 只能绑定 agent_instance 或 workload Principal")
			return
		}
		if h.projects == nil || input.ProjectID == "" {
			jsonError(w, http.StatusBadRequest, "INVALID_BINDING_PROJECT", "AGENT Binding 必须绑定 PAM project")
			return
		}
		project, projectErr := h.projects.GetByID(r.Context(), input.ProjectID)
		if projectErr != nil || project == nil || project.TenantID != actor.TenantID {
			jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "PAM project 不存在")
			return
		}
	} else if target.Kind != domain.PrincipalKindUser {
		jsonError(w, http.StatusBadRequest, "INVALID_BINDING_PRINCIPAL", "USER Binding 只能绑定 user Principal")
		return
	}
	binding := &domain.ExternalIdentityBinding{
		Source: input.Source, WorkspaceID: input.WorkspaceID, ExternalProjectID: input.ExternalProjectID,
		IdentityType: input.IdentityType, ExternalID: input.ExternalID, PrincipalID: input.PrincipalID,
		ProjectID: input.ProjectID, Status: domain.ExternalBindingActive, CreatedBy: actor.Principal.ID,
	}
	if err := h.externalIdentities.Create(r.Context(), binding); err != nil {
		jsonError(w, http.StatusConflict, "EXTERNAL_BINDING_CREATE_FAILED", "外部身份 Binding 已存在或数据无效")
		return
	}
	h.writeExternalIdentityAudit(r.Context(), "external_binding.created", binding.ID, map[string]any{
		"binding_principal_id": binding.PrincipalID, "binding_principal_name": target.Name,
	})
	jsonResp(w, http.StatusCreated, binding)
}

func (h *Handler) ListExternalBindings(w http.ResponseWriter, r *http.Request) {
	if h.externalIdentities == nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "外部身份 Binding 尚未初始化")
		return
	}
	items, err := h.externalIdentities.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询外部身份 Binding 失败")
		return
	}
	if items == nil {
		items = []domain.ExternalIdentityBinding{}
	}
	jsonResp(w, http.StatusOK, items)
}

func (h *Handler) RevokeExternalBinding(w http.ResponseWriter, r *http.Request) {
	if h.externalIdentities == nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "外部身份 Binding 尚未初始化")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "id 为必填")
		return
	}
	actor := identityFromContext(r.Context())
	ok, err := h.externalIdentities.Revoke(r.Context(), id, actor.Principal.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "EXTERNAL_BINDING_REVOKE_FAILED", "撤销外部身份 Binding 失败")
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "EXTERNAL_BINDING_NOT_FOUND", "外部身份 Binding 不存在或已撤销")
		return
	}
	h.writeExternalIdentityAudit(r.Context(), "external_binding.revoked", id, nil)
	jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": string(domain.ExternalBindingRevoked)})
}

func (h *Handler) RevokeExternalWorkspace(w http.ResponseWriter, r *http.Request) {
	if h.externalIdentities == nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "外部身份 Binding 尚未初始化")
		return
	}
	var input struct {
		Source      string `json:"source"`
		WorkspaceID string `json:"workspaceId"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_WORKSPACE_REVOKE", "请求体解析失败")
		return
	}
	input.Source = strings.TrimSpace(input.Source)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if !externalContextSourcePattern.MatchString(input.Source) || input.WorkspaceID == "" || len(input.WorkspaceID) > 255 || !externalContextIDPattern.MatchString(input.WorkspaceID) {
		jsonError(w, http.StatusBadRequest, "INVALID_WORKSPACE_REVOKE", "source 或 workspaceId 格式非法")
		return
	}
	revoked, err := h.externalIdentities.RevokeWorkspace(r.Context(), input.Source, input.WorkspaceID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "EXTERNAL_WORKSPACE_REVOKE_FAILED", "撤销 Workspace 外部身份失败")
		return
	}
	h.writeExternalIdentityAudit(r.Context(), "external_binding.workspace_revoked", input.Source+"/"+input.WorkspaceID, map[string]any{
		"source": input.Source, "workspace_id": input.WorkspaceID, "revoked_bindings": revoked,
	})
	jsonResp(w, http.StatusOK, map[string]any{"source": input.Source, "workspaceId": input.WorkspaceID, "revokedBindings": revoked})
}

func (h *Handler) RevokeDelegationToken(w http.ResponseWriter, r *http.Request) {
	if h.delegationTokens == nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "委托 Token 尚未初始化")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "id 为必填")
		return
	}
	if err := h.delegationTokens.Revoke(r.Context(), id); err != nil {
		jsonError(w, http.StatusNotFound, "DELEGATION_TOKEN_NOT_FOUND", "委托 Token 不存在或已撤销")
		return
	}
	h.writeExternalIdentityAudit(r.Context(), "delegation.revoked", id, nil)
	jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "REVOKED"})
}

func (h *Handler) writeExternalIdentityAudit(ctx context.Context, action, targetID string, metadata map[string]any) {
	if h.audit == nil {
		return
	}
	actor := identityFromContext(ctx)
	event := &domain.AuditEvent{Action: action, TargetID: targetID, Summary: action, Metadata: map[string]any{}}
	if actor != nil && actor.Principal != nil {
		event.ActorID, event.ActorType, event.CredentialID = actor.Principal.ID, string(actor.Principal.Kind), actor.CredentialID
	}
	for key, value := range metadata {
		event.Metadata[key] = value
	}
	_ = h.audit.Create(ctx, event)
}

type exchangeDelegationRequest struct {
	ExternalContext domain.ExternalExecutionContext `json:"externalContext"`
	TTLSeconds      int                             `json:"ttlSeconds"`
}

func (h *Handler) ExchangeDelegationToken(w http.ResponseWriter, r *http.Request) {
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil || identity.CredentialID == "" ||
		(identity.Principal.Kind != domain.PrincipalKindAgentInstance && identity.Principal.Kind != domain.PrincipalKindWorkload) {
		jsonError(w, http.StatusForbidden, "DELEGATION_PRINCIPAL_REQUIRED", "只有 Agent Principal 的长期 Key 可以申请委托 Token")
		return
	}
	if h.externalIdentities == nil || h.delegationTokens == nil {
		jsonError(w, http.StatusServiceUnavailable, "NOT_READY", "委托 Token 尚未初始化")
		return
	}
	var req exchangeDelegationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_DELEGATION_REQUEST", "请求体解析失败")
		return
	}
	externalContext, err := normalizeExternalExecutionContext(&req.ExternalContext)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_EXTERNAL_CONTEXT", err.Error())
		return
	}
	getString := func(key string) string {
		value, _ := externalContext[key].(string)
		return value
	}
	if getString("workspaceId") == "" || getString("agentId") == "" || getString("taskId") == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_DELEGATION_CONTEXT", "委托 Token 必须包含 workspaceId、agentId 和 taskId")
		return
	}
	if getString("originatorSource") == "human" && getString("originatorUserId") == "" {
		jsonError(w, http.StatusBadRequest, "ORIGINATOR_REQUIRED", "human 发起的委托必须提供 originatorUserId")
		return
	}
	if getString("projectId") == "" {
		jsonError(w, http.StatusBadRequest, "EXTERNAL_PROJECT_REQUIRED", "委托 Token 必须包含 externalContext.projectId")
		return
	}
	contextValue := domain.ExternalExecutionContext{
		Source: getString("source"), WorkspaceID: getString("workspaceId"), ProjectID: getString("projectId"),
		AgentID: getString("agentId"), TaskID: getString("taskId"), IssueID: getString("issueId"),
		OriginatorUserID: getString("originatorUserId"), AccountableUserID: getString("accountableUserId"),
		OriginatorSource: getString("originatorSource"), DelegatedFromTaskID: getString("delegatedFromTaskId"),
	}
	binding, err := h.externalIdentities.GetAgent(r.Context(), contextValue.Source, contextValue.WorkspaceID, contextValue.ProjectID, contextValue.AgentID)
	if err != nil || binding == nil || binding.PrincipalID != identity.Principal.ID {
		jsonError(w, http.StatusForbidden, "EXTERNAL_AGENT_NOT_BOUND", "当前 PAM Principal 未绑定该外部 Agent")
		return
	}
	if accessErr := h.principalProjectAccess(r.Context(), binding.ProjectID, identity.Principal.ID, true); accessErr != nil {
		var membershipErr *projectMembershipAccessError
		if errors.As(accessErr, &membershipErr) {
			jsonError(w, http.StatusForbidden, membershipErr.Code, membershipErr.Message)
		} else {
			jsonError(w, http.StatusInternalServerError, "PROJECT_ACCESS_CHECK_FAILED", "检查 Agent 的 PAM project 权限失败")
		}
		return
	}
	var onBehalfOfPrincipalID, accountablePrincipalID string
	userMappings := []struct {
		label      string
		externalID string
		execute    bool
		target     *string
	}{
		{label: "originatorUserId", externalID: contextValue.OriginatorUserID, execute: true, target: &onBehalfOfPrincipalID},
		{label: "accountableUserId", externalID: contextValue.AccountableUserID, execute: false, target: &accountablePrincipalID},
	}
	for _, mapping := range userMappings {
		label, externalID := mapping.label, mapping.externalID
		if externalID == "" {
			continue
		}
		userBinding, userErr := h.externalIdentities.GetUser(r.Context(), contextValue.Source, contextValue.WorkspaceID, contextValue.ProjectID, externalID)
		if userErr != nil || userBinding == nil {
			jsonError(w, http.StatusForbidden, "EXTERNAL_USER_NOT_BOUND", fmt.Sprintf("%s 未绑定 PAM user Principal", label))
			return
		}
		user, userErr := h.principals.GetByID(r.Context(), userBinding.PrincipalID)
		if userErr != nil || user == nil || user.Kind != domain.PrincipalKindUser || user.Status != domain.PrincipalStatusActive {
			jsonError(w, http.StatusForbidden, "EXTERNAL_USER_NOT_ACTIVE", fmt.Sprintf("%s 对应的 PAM user Principal 不可用", label))
			return
		}
		if accessErr := h.principalProjectAccess(r.Context(), binding.ProjectID, user.ID, mapping.execute); accessErr != nil {
			var membershipErr *projectMembershipAccessError
			if errors.As(accessErr, &membershipErr) {
				jsonError(w, http.StatusForbidden, "ON_BEHALF_OF_"+membershipErr.Code, membershipErr.Message)
			} else {
				jsonError(w, http.StatusInternalServerError, "ON_BEHALF_OF_PROJECT_CHECK_FAILED", "检查代表用户的 PAM project 权限失败")
			}
			return
		}
		*mapping.target = user.ID
	}
	ttl := defaultDelegationTokenTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if ttl <= 0 || ttl > maxDelegationTokenTTL {
		jsonError(w, http.StatusBadRequest, "INVALID_DELEGATION_TTL", "委托 Token 有效期必须不超过 15 分钟")
		return
	}
	scopes := make([]string, 0, len(defaultAgentScopes)-1)
	for _, scope := range defaultAgentScopes {
		if scope != "pam:delegation:exchange" && identity.HasScope(scope) {
			scopes = append(scopes, scope)
		}
	}
	if !hasScopeValue(scopes, "pam:executions:create") {
		jsonError(w, http.StatusForbidden, "INSUFFICIENT_DELEGATION_SCOPE", "长期 Key 没有执行权限")
		return
	}
	tokenID, plaintext, hash, err := generateDelegationToken()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DELEGATION_TOKEN_CREATE_FAILED", "生成委托 Token 失败")
		return
	}
	now := time.Now()
	token := &domain.DelegationToken{ID: tokenID, ParentCredentialID: identity.CredentialID, PrincipalID: identity.Principal.ID,
		OnBehalfOfPrincipalID: onBehalfOfPrincipalID, AccountablePrincipalID: accountablePrincipalID,
		TokenHash: hash, Scopes: scopes, ExternalContext: externalContext, ActingProjectID: binding.ProjectID,
		IssuedAt: now, ExpiresAt: now.Add(ttl)}
	if err := h.delegationTokens.Create(r.Context(), token); err != nil {
		jsonError(w, http.StatusInternalServerError, "DELEGATION_TOKEN_CREATE_FAILED", "保存委托 Token 失败")
		return
	}
	if h.audit != nil {
		_ = h.audit.Create(r.Context(), &domain.AuditEvent{Action: "delegation.issued", ActorID: identity.Principal.ID,
			ActorType: string(identity.Principal.Kind), CredentialID: identity.CredentialID, TargetID: token.ID,
			ActingProjectID: binding.ProjectID, Summary: "delegation.issued", Metadata: map[string]any{
				"parent_credential_id": identity.CredentialID, "external_context": externalContext, "expires_at": token.ExpiresAt,
				"on_behalf_of_principal_id": onBehalfOfPrincipalID, "accountable_principal_id": accountablePrincipalID,
			}})
	}
	jsonResp(w, http.StatusCreated, map[string]any{"delegationToken": plaintext, "tokenId": token.ID, "expiresAt": token.ExpiresAt,
		"scopes": scopes, "actingProjectId": binding.ProjectID, "externalContext": externalContext,
		"onBehalfOfPrincipalId": onBehalfOfPrincipalID, "accountablePrincipalId": accountablePrincipalID,
		"warning": "委托 Token 只显示一次，默认 5 分钟后过期；不要写入 Agent 环境变量或日志"})
}

func generateDelegationToken() (id, plaintext string, hash []byte, err error) {
	idBytes := make([]byte, 16)
	secret := make([]byte, 32)
	if _, err = rand.Read(idBytes); err != nil {
		return "", "", nil, err
	}
	if _, err = rand.Read(secret); err != nil {
		return "", "", nil, err
	}
	idBytes[6] = (idBytes[6] & 0x0f) | 0x40
	idBytes[8] = (idBytes[8] & 0x3f) | 0x80
	id = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", idBytes[0:4], idBytes[4:6], idBytes[6:8], idBytes[8:10], idBytes[10:16])
	secretText := base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(secretText))
	return id, "pam_dtk_" + id + "_" + secretText, digest[:], nil
}

func parseDelegationToken(value string) (string, string, bool) {
	parts := strings.SplitN(value, "_", 4)
	if len(parts) != 4 || parts[0] != "pam" || parts[1] != "dtk" || parts[2] == "" || parts[3] == "" {
		return "", "", false
	}
	return parts[2], parts[3], true
}
