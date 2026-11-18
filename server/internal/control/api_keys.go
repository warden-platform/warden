package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

var defaultAgentScopes = []string{
	"warden:resources:read", "warden:executions:create", "warden:executions:read:self", "warden:executions:cancel:self", "warden:delegation:exchange",
}

var registrationCredentialScopes = []string{"warden:registration:read", "warden:registration:write"}

const (
	defaultRegistrationCredentialTTL = 30 * 24 * time.Hour
	maxRegistrationCredentialTTL     = 90 * 24 * time.Hour
)

func generateAPIKey() (id, plaintext string, hash []byte, err error) {
	return generatePurposeAPIKey(domain.APIKeyPurposeAgent)
}

func generatePurposeAPIKey(purpose domain.APIKeyPurpose) (id, plaintext string, hash []byte, err error) {
	if purpose != domain.APIKeyPurposeAgent && purpose != domain.APIKeyPurposeRegistration {
		return "", "", nil, errors.New("unsupported API key purpose")
	}
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
	prefix := "agk"
	if purpose == domain.APIKeyPurposeRegistration {
		prefix = "rgk"
	}
	plaintext = "warden_" + prefix + "_" + id + "_" + secretText
	digest := sha256.Sum256([]byte(secretText))
	return id, plaintext, digest[:], nil
}

func parseAPIKey(value string) (string, string, bool) {
	id, secret, purpose, ok := parsePurposeAPIKey(value)
	return id, secret, ok && purpose == domain.APIKeyPurposeAgent
}

func parsePurposeAPIKey(value string) (string, string, domain.APIKeyPurpose, bool) {
	parts := strings.SplitN(value, "_", 4)
	if len(parts) != 4 || parts[0] != "warden" || parts[2] == "" || parts[3] == "" {
		return "", "", "", false
	}
	purpose := domain.APIKeyPurposeAgent
	if parts[1] == "rgk" {
		purpose = domain.APIKeyPurposeRegistration
	} else if parts[1] != "agk" {
		return "", "", "", false
	}
	return parts[2], parts[3], purpose, true
}

func (h *Handler) authenticate(ctx context.Context, token, remoteAddr string) (*AuthenticatedIdentity, error) {
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.adminAPIKey)) == 1 {
		if h.principals == nil {
			return &AuthenticatedIdentity{Principal: &domain.Principal{ID: "00000000-0000-0000-0000-000000000003", Kind: domain.PrincipalKindUser, Name: "admin", Status: domain.PrincipalStatusActive, TenantID: "platform"}, TenantID: "platform", IsPlatformAdmin: true, Scopes: []string{"warden:admin"}, Kind: "ADMIN"}, nil
		}
		principal, err := h.principals.GetByID(domain.WithTenantID(ctx, "platform"), "00000000-0000-0000-0000-000000000003")
		if err != nil {
			return nil, err
		}
		if principal == nil || principal.Status != domain.PrincipalStatusActive {
			return nil, errors.New("admin principal is unavailable")
		}
		if !h.tenantIsActive(ctx, principal.TenantID) {
			return nil, errors.New("tenant is disabled")
		}
		return &AuthenticatedIdentity{Principal: principal, TenantID: principal.TenantID, IsPlatformAdmin: true, Scopes: []string{"warden:admin"}, Kind: "ADMIN"}, nil
	}
	// JWT branch: a JWT has the shape "xxx.yyy.zzz" (3 base64 segments
	// separated by dots). We try parsing as JWT first; on failure we fall
	// through to the warden_agk_ API key path so existing agents keep working.
	if strings.Count(token, ".") == 2 {
		if claims, err := h.parseJWT(token); err == nil && claims != nil {
			if strings.TrimSpace(claims.Tenant) == "" && h.pool != nil {
				return nil, nil
			}
			if strings.TrimSpace(claims.Tenant) != "" {
				ctx = domain.WithTenantID(ctx, claims.Tenant)
			}
			if h.pool != nil {
				var active bool
				if err := h.pool.QueryRow(ctx, `SELECT revoked_at IS NULL AND expires_at>now() FROM admin_sessions WHERE jti=$1 AND principal_id=$2`, claims.ID, claims.Subject).Scan(&active); err != nil || !active {
					return nil, nil
				}
			}
			principal, err := h.principals.GetByID(ctx, claims.Subject)
			if err != nil || principal == nil || principal.Status != domain.PrincipalStatusActive {
				return nil, nil
			}
			if !h.tenantIsActive(ctx, principal.TenantID) {
				return nil, nil
			}
			scopes := claims.Scope
			// JWT jti identifies an admin session, not a principal_api_keys UUID.
			// Keeping the namespaces separate prevents session IDs from being
			// written into executions/audit_events.credential_id UUID columns.
			return &AuthenticatedIdentity{Principal: principal, TenantID: principal.TenantID, IsPlatformAdmin: hasScopeValue(scopes, "warden:admin"), SessionID: claims.ID, Scopes: scopes, Kind: "JWT"}, nil
		}
	}
	if tokenID, secret, ok := parseDelegationToken(token); ok && h.delegationTokens != nil {
		delegation, principal, err := h.delegationTokens.GetForAuthentication(ctx, tokenID)
		if err != nil || delegation == nil || principal == nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(secret))
		if subtle.ConstantTimeCompare(digest[:], delegation.TokenHash) != 1 {
			return nil, nil
		}
		if !h.tenantIsActive(ctx, principal.TenantID) {
			return nil, nil
		}
		ip := remoteAddr
		if host, _, splitErr := net.SplitHostPort(remoteAddr); splitErr == nil {
			ip = host
		}
		if parsed := net.ParseIP(ip); parsed == nil {
			ip = ""
		}
		_ = h.delegationTokens.MarkUsed(domain.WithTenantID(ctx, principal.TenantID), delegation.ID)
		return &AuthenticatedIdentity{Principal: principal, TenantID: principal.TenantID,
			CredentialID: delegation.ParentCredentialID, DelegationTokenID: delegation.ID,
			DelegationProjectID: delegation.ActingProjectID, DelegationExternalContext: delegation.ExternalContext,
			OnBehalfOfPrincipalID: delegation.OnBehalfOfPrincipalID, AccountablePrincipalID: delegation.AccountablePrincipalID,
			Scopes: delegation.Scopes, Kind: "DELEGATED_AGENT", AuditMetadata: map[string]any{
				"delegation_token_id": delegation.ID, "parent_credential_id": delegation.ParentCredentialID,
				"on_behalf_of_principal_id": delegation.OnBehalfOfPrincipalID, "accountable_principal_id": delegation.AccountablePrincipalID,
				"delegation_expires_at": delegation.ExpiresAt, "remote_ip": ip,
			}}, nil
	}
	id, secret, tokenPurpose, ok := parsePurposeAPIKey(token)
	if !ok || h.apiKeys == nil {
		return nil, nil
	}
	key, principal, err := h.apiKeys.GetForAuthentication(ctx, id)
	if err != nil || key == nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(digest[:], key.KeyHash) != 1 || key.Status != domain.APIKeyActive ||
		principal.Status != domain.PrincipalStatusActive || (key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now())) {
		return nil, nil
	}
	if !h.tenantIsActive(ctx, principal.TenantID) {
		return nil, nil
	}
	if key.Purpose == "" {
		key.Purpose = domain.APIKeyPurposeAgent
	}
	if key.Purpose != tokenPurpose {
		return nil, nil
	}
	if key.Purpose == domain.APIKeyPurposeRegistration &&
		(principal.Kind != domain.PrincipalKindWorkload || h.registrationAudience == "" || key.Audience != h.registrationAudience) {
		h.writeRegistrationAuthenticationDenied(ctx, key, principal, "REGISTRATION_AUDIENCE_MISMATCH")
		return nil, nil
	}
	ip := remoteAddr
	if host, _, splitErr := net.SplitHostPort(remoteAddr); splitErr == nil {
		ip = host
	}
	if parsed := net.ParseIP(ip); parsed == nil {
		ip = ""
	}
	_ = h.apiKeys.MarkUsed(ctx, key.ID, ip)
	kind := "AGENT"
	if key.Purpose == domain.APIKeyPurposeRegistration {
		kind = "REGISTRATION"
	}
	return &AuthenticatedIdentity{Principal: principal, TenantID: principal.TenantID, CredentialID: key.ID, Scopes: key.Scopes, Kind: kind,
		AuditMetadata: map[string]any{"credential_purpose": key.Purpose, "credential_audience": key.Audience}}, nil
}

func (h *Handler) tenantIsActive(ctx context.Context, tenantID string) bool {
	// The unaffiliated sentinel is not a real tenant row but is a valid,
	// authenticated state (a SSO identity awaiting an organization), so it must
	// pass the activity check. Empty means "no tenant context" and is allowed by
	// every other guard's own tenant isolation.
	if h.pool == nil || tenantID == "" || isUnaffiliated(tenantID) {
		return true
	}
	var active bool
	if err := h.pool.QueryRow(ctx, `SELECT status='ACTIVE' FROM tenants WHERE slug=$1`, tenantID).Scan(&active); err != nil {
		return false
	}
	return active
}

func hasScopeValue(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

type createPrincipalRequest struct {
	Name     string               `json:"name"`
	Kind     domain.PrincipalKind `json:"kind"`
	Metadata map[string]any       `json:"metadata"`
}

func (h *Handler) CreatePrincipal(w http.ResponseWriter, r *http.Request) {
	var req createPrincipalRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonError(w, 400, "INVALID_PRINCIPAL", "name 为必填")
		return
	}
	// Only the account administration endpoint may assign a role. Stripping
	// it here prevents a tenant admin from planting an elevated marker on a
	// machine identity created through the principal API.
	delete(req.Metadata, "role")
	if req.Kind == "" {
		req.Kind = domain.PrincipalKindAgentInstance
	}
	if req.Kind != domain.PrincipalKindAgentInstance && req.Kind != domain.PrincipalKindWorkload {
		jsonError(w, 400, "INVALID_PRINCIPAL_KIND", "kind 仅支持 agent_instance 或 workload")
		return
	}
	actor := identityFromContext(r.Context())
	tenantID := "platform"
	if actor != nil && actor.TenantID != "" {
		tenantID = actor.TenantID
	}
	if raw, exists := req.Metadata[principalProjectIDsMetadataKey]; exists {
		projects, _, err := principalProjectScope(&AuthenticatedIdentity{Principal: &domain.Principal{Metadata: map[string]any{principalProjectIDsMetadataKey: raw}}})
		if err != nil || len(projects) > 100 {
			jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_SCOPE", "metadata.project_ids 必须是最多 100 个项目 ID 的字符串数组")
			return
		}
		for projectID := range projects {
			project, projectErr := h.projects.GetByID(r.Context(), projectID)
			if projectErr != nil || project == nil {
				jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_SCOPE", "metadata.project_ids 包含不存在的项目")
				return
			}
		}
	}
	p := &domain.Principal{Kind: req.Kind, Name: strings.TrimSpace(req.Name), TenantID: tenantID, Metadata: req.Metadata}
	if err := h.principals.Create(r.Context(), p); err != nil {
		jsonError(w, 409, "PRINCIPAL_CREATE_FAILED", "Principal 名称已存在或数据无效")
		return
	}
	h.writeIdentityAudit(r.Context(), "principal.created", p.ID, "")
	jsonResp(w, http.StatusCreated, p)
}

func (h *Handler) ListPrincipals(w http.ResponseWriter, r *http.Request) {
	items, err := h.principals.List(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "查询 Principal 失败")
		return
	}
	jsonResp(w, 200, items)
}

func (h *Handler) DeletePrincipal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, 400, "INVALID_REQUEST", "id 为必填")
		return
	}
	actor := identityFromContext(r.Context())
	if actor != nil && actor.Principal != nil && actor.Principal.ID == id {
		jsonError(w, 409, "SELF_DELETE_FORBIDDEN", "不能删除当前登录身份")
		return
	}
	p, err := h.principals.GetByID(r.Context(), id)
	if err != nil || p == nil {
		jsonError(w, 404, "PRINCIPAL_NOT_FOUND", "Principal 不存在")
		return
	}
	if p.Kind == domain.PrincipalKindUser {
		jsonError(w, 409, "CANNOT_DELETE_USER", "不能删除用户类型 Principal")
		return
	}
	if err := h.principals.Delete(r.Context(), id); err != nil {
		jsonError(w, 500, "PRINCIPAL_DELETE_FAILED", "删除 Principal 失败")
		return
	}
	h.writeIdentityAuditNamed(r.Context(), "principal.deleted", id, p.Name, "")
	jsonResp(w, 200, map[string]any{"id": id, "status": "DISABLED"})
}

type createAPIKeyRequest struct {
	Name      string               `json:"name"`
	Scopes    []string             `json:"scopes"`
	ExpiresAt *time.Time           `json:"expiresAt"`
	Purpose   domain.APIKeyPurpose `json:"-"`
	Audience  string               `json:"-"`
}

func validateAgentScopes(scopes []string) bool {
	allowed := map[string]bool{}
	for _, s := range defaultAgentScopes {
		allowed[s] = true
	}
	for _, s := range scopes {
		if !allowed[s] {
			return false
		}
	}
	return len(scopes) > 0
}

func (h *Handler) createCredential(ctx context.Context, principalID string, req createAPIKeyRequest, parentID string, actor *AuthenticatedIdentity) (*domain.PrincipalAPIKey, string, error) {
	p, err := h.principals.GetByID(ctx, principalID)
	if err != nil || p == nil {
		return nil, "", errors.New("principal not found")
	}
	if req.Purpose == "" {
		req.Purpose = domain.APIKeyPurposeAgent
	}
	if req.Purpose == domain.APIKeyPurposeRegistration {
		if p.Kind != domain.PrincipalKindWorkload {
			return nil, "", errors.New("registration credentials require a workload principal")
		}
		if purpose, _ := p.Metadata["purpose"].(string); purpose != "warden_resource_registration" {
			return nil, "", errors.New("workload principal is not designated for Warden resource registration")
		}
		if req.Audience == "" || req.Audience != h.registrationAudience {
			return nil, "", errors.New("registration credential audience does not match this Warden environment")
		}
		if !sameScopes(req.Scopes, registrationCredentialScopes) {
			return nil, "", errors.New("invalid registration scopes")
		}
		if req.Name == "" {
			req.Name = "warden-resource-registration"
		}
	} else {
		if p.Kind != domain.PrincipalKindAgentInstance && p.Kind != domain.PrincipalKindWorkload {
			return nil, "", errors.New("principal kind cannot own agent key")
		}
		if req.Name == "" {
			req.Name = "multica-agent"
		}
		if len(req.Scopes) == 0 {
			req.Scopes = append([]string(nil), defaultAgentScopes...)
		}
		if !validateAgentScopes(req.Scopes) {
			return nil, "", errors.New("invalid agent scopes")
		}
	}
	id, plaintext, hash, err := generatePurposeAPIKey(req.Purpose)
	if err != nil {
		return nil, "", err
	}
	key := &domain.PrincipalAPIKey{ID: id, PrincipalID: principalID, Name: req.Name, KeyPrefix: plaintext[:16], KeyHash: hash,
		Status: domain.APIKeyActive, Purpose: req.Purpose, Audience: req.Audience, Scopes: req.Scopes, ExpiresAt: req.ExpiresAt,
		CreatedBy: actor.Principal.ID, RotationParentID: parentID}
	if err := h.apiKeys.Create(ctx, key); err != nil {
		return nil, "", err
	}
	return key, plaintext, nil
}

func sameScopes(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	wanted := make(map[string]struct{}, len(expected))
	for _, scope := range expected {
		wanted[scope] = struct{}{}
	}
	for _, scope := range actual {
		if _, ok := wanted[scope]; !ok {
			return false
		}
		delete(wanted, scope)
	}
	return len(wanted) == 0
}

func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	key, plaintext, err := h.createCredential(r.Context(), r.PathValue("id"), req, "", identityFromContext(r.Context()))
	if err != nil {
		jsonError(w, 400, "CREDENTIAL_CREATE_FAILED", err.Error())
		return
	}
	h.writeIdentityAudit(r.Context(), "credential.created", key.PrincipalID, key.ID)
	jsonResp(w, 201, map[string]any{"credential": key, "apiKey": plaintext, "warning": "API Key 只显示一次，请立即保存"})
}

func (h *Handler) CreateRegistrationAPIKey(w http.ResponseWriter, r *http.Request) {
	if h.registrationAudience == "" {
		jsonError(w, http.StatusServiceUnavailable, "REGISTRATION_AUDIENCE_NOT_CONFIGURED", "服务端未配置资源注册凭据 audience")
		return
	}
	var input struct {
		Name      string     `json:"name"`
		Audience  string     `json:"audience"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	audience := h.registrationAudience
	if strings.TrimSpace(input.Audience) != "" {
		normalized, err := normalizeRegistrationAudience(input.Audience)
		if err != nil || normalized != audience {
			jsonError(w, http.StatusBadRequest, "REGISTRATION_AUDIENCE_MISMATCH", "audience 必须与当前 Warden API Endpoint 完全一致")
			return
		}
	}
	now := time.Now()
	expiresAt := input.ExpiresAt
	if expiresAt == nil {
		value := now.Add(defaultRegistrationCredentialTTL)
		expiresAt = &value
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(maxRegistrationCredentialTTL)) {
		jsonError(w, http.StatusBadRequest, "INVALID_CREDENTIAL_EXPIRY", "注册凭据有效期必须在当前时间之后且不超过 90 天")
		return
	}
	key, plaintext, err := h.createCredential(r.Context(), r.PathValue("id"), createAPIKeyRequest{
		Name: strings.TrimSpace(input.Name), Scopes: append([]string(nil), registrationCredentialScopes...), ExpiresAt: expiresAt,
		Purpose: domain.APIKeyPurposeRegistration, Audience: audience,
	}, "", identityFromContext(r.Context()))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "REGISTRATION_CREDENTIAL_CREATE_FAILED", err.Error())
		return
	}
	h.writeIdentityAudit(r.Context(), "credential.created", key.PrincipalID, key.ID)
	jsonResp(w, http.StatusCreated, map[string]any{"credential": key, "apiKey": plaintext,
		"apiEndpoint": audience, "warning": "Registration Key 只显示一次，请立即注入指定机器人并删除临时副本"})
}

func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	actor := identityFromContext(r.Context())
	if actor == nil || actor.Principal == nil {
		jsonError(w, http.StatusForbidden, "TENANT_CONTEXT_REQUIRED", "租户身份缺失")
		return
	}
	target, err := h.principals.GetByID(r.Context(), r.PathValue("id"))
	if err != nil || target == nil || target.TenantID != actor.TenantID {
		jsonError(w, http.StatusNotFound, "PRINCIPAL_NOT_FOUND", "Principal 不存在")
		return
	}
	items, err := h.apiKeys.ListByPrincipal(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "查询 API Key 失败")
		return
	}
	if items == nil {
		items = []domain.PrincipalAPIKey{}
	}
	jsonResp(w, 200, items)
}

func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	actor := identityFromContext(r.Context())
	credentialID := r.PathValue("id")
	key, _ := h.apiKeys.GetByID(r.Context(), credentialID)
	if key == nil {
		jsonError(w, http.StatusNotFound, "CREDENTIAL_NOT_FOUND", "API Key 不存在")
		return
	}
	target, _ := h.principals.GetByID(r.Context(), key.PrincipalID)
	if target == nil || actor == nil || target.TenantID != actor.TenantID {
		jsonError(w, http.StatusNotFound, "CREDENTIAL_NOT_FOUND", "API Key 不存在")
		return
	}
	ok, err := h.apiKeys.Revoke(r.Context(), credentialID, actor.Principal.ID)
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "撤销 API Key 失败")
		return
	}
	if !ok {
		jsonError(w, 409, "CREDENTIAL_NOT_ACTIVE", "API Key 不存在或已撤销")
		return
	}
	principalID := ""
	if key != nil {
		principalID = key.PrincipalID
	}
	h.writeIdentityAudit(r.Context(), "credential.revoked", principalID, credentialID)
	jsonResp(w, 200, map[string]string{"id": credentialID, "status": "REVOKED"})
}

func (h *Handler) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	old, err := h.apiKeys.GetByID(r.Context(), r.PathValue("id"))
	if err != nil || old == nil || old.Status != domain.APIKeyActive {
		jsonError(w, 409, "CREDENTIAL_NOT_ACTIVE", "API Key 不存在或已撤销")
		return
	}
	target, _ := h.principals.GetByID(r.Context(), old.PrincipalID)
	actor := identityFromContext(r.Context())
	if target == nil || actor == nil || target.TenantID != actor.TenantID {
		jsonError(w, http.StatusNotFound, "CREDENTIAL_NOT_FOUND", "API Key 不存在")
		return
	}
	key, plaintext, err := h.createCredential(r.Context(), old.PrincipalID, createAPIKeyRequest{Name: old.Name + " (rotated)", Scopes: old.Scopes,
		ExpiresAt: old.ExpiresAt, Purpose: old.Purpose, Audience: old.Audience}, old.ID, identityFromContext(r.Context()))
	if err != nil {
		jsonError(w, 500, "CREDENTIAL_ROTATE_FAILED", "创建轮换 Key 失败")
		return
	}
	if ok, err := h.apiKeys.Revoke(r.Context(), old.ID, identityFromContext(r.Context()).Principal.ID); err != nil || !ok {
		_, _ = h.apiKeys.Revoke(r.Context(), key.ID, identityFromContext(r.Context()).Principal.ID)
		jsonError(w, 409, "CREDENTIAL_ROTATE_FAILED", "旧 Key 状态已变化")
		return
	}
	h.writeIdentityAudit(r.Context(), "credential.rotated", old.PrincipalID, key.ID)
	jsonResp(w, 201, map[string]any{"credential": key, "apiKey": plaintext, "replaces": old.ID, "warning": "API Key 只显示一次，请立即保存"})
}

func (h *Handler) writeIdentityAudit(ctx context.Context, action, targetPrincipalID, credentialID string) {
	targetName := ""
	if targetPrincipalID != "" {
		if target, err := h.principals.GetByID(ctx, targetPrincipalID); err == nil && target != nil {
			targetName = target.Name
		}
	}
	h.writeIdentityAuditNamed(ctx, action, targetPrincipalID, targetName, credentialID)
}

func (h *Handler) writeIdentityAuditNamed(ctx context.Context, action, targetPrincipalID, targetName, credentialID string) {
	if h.audit == nil {
		return
	}
	actor := identityFromContext(ctx)
	metadata := map[string]any{}
	summary := action
	if targetPrincipalID != "" {
		metadata["target_principal_id"] = targetPrincipalID
	}
	if targetName != "" {
		metadata["target_principal_name"] = targetName
		summary = action + " · " + targetName
	}
	e := &domain.AuditEvent{Action: action, TargetID: targetPrincipalID, CredentialID: credentialID, Summary: summary, Metadata: metadata}
	if actor != nil && actor.Principal != nil {
		e.ActorID = actor.Principal.ID
		e.ActorType = string(actor.Principal.Kind)
	}
	if err := h.audit.Create(ctx, e); err != nil {
		log.Printf("写入身份审计事件失败 action=%s target=%s: %v", action, targetPrincipalID, err)
	}
}
