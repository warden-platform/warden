package control

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gitee.com/pam-platform/pam/server/internal/config"
	"gitee.com/pam-platform/pam/server/internal/domain"
	"gitee.com/pam-platform/pam/server/internal/storage/postgres"
	"github.com/golang-jwt/jwt/v5"
)

const federatedAssertionSource = "federated"

type federatedAssertionClaims struct {
	EnterpriseID  string `json:"enterprise_id"`
	WorkspaceID   string `json:"workspace_id"`
	ProjectID     string `json:"project_id,omitempty"`
	Email         string `json:"email,omitempty"`
	DisplayName   string `json:"name,omitempty"`
	WorkspaceRole string `json:"workspace_role,omitempty"`
	Nonce         string `json:"nonce"`
	jwt.RegisteredClaims
}

type federatedAssertionVerifier struct {
	key      any
	issuer   string
	audience string
}

func loadFederatedAssertionVerifier() (*federatedAssertionVerifier, error) {
	keyText, err := config.Secret("PAM_FEDERATED_ASSERTION_PUBLIC_KEY")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(keyText) == "" {
		return nil, nil
	}
	block, _ := pem.Decode([]byte(keyText))
	if block == nil {
		return nil, errors.New("PAM_FEDERATED_ASSERTION_PUBLIC_KEY is not PEM encoded")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// RSA public keys are sometimes distributed in PKCS#1 form.
		if rsaKey, rsaErr := x509.ParsePKCS1PublicKey(block.Bytes); rsaErr == nil {
			key = rsaKey
		} else {
			return nil, fmt.Errorf("parse federated assertion public key: %w", err)
		}
	}
	switch key.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey:
	default:
		return nil, errors.New("federated assertion key must be RSA, ECDSA, or Ed25519")
	}
	issuer := strings.TrimSpace(getenvDefault("PAM_FEDERATED_ASSERTION_ISSUER", "https://idp.example.com"))
	audience := strings.TrimSpace(getenvDefault("PAM_FEDERATED_ASSERTION_AUDIENCE", "pam"))
	if issuer == "" || audience == "" {
		return nil, errors.New("federated assertion issuer and audience must not be empty")
	}
	return &federatedAssertionVerifier{key: key, issuer: issuer, audience: audience}, nil
}

func getenvDefault(name, fallback string) string {
	if value, err := config.Secret(name); err == nil && value != "" {
		return value
	}
	return fallback
}

func (v *federatedAssertionVerifier) verify(raw string) (*federatedAssertionClaims, error) {
	if v == nil {
		return nil, errors.New("federated assertion verifier is not configured")
	}
	claims := &federatedAssertionClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method == nil {
			return nil, errors.New("missing assertion signing method")
		}
		switch v.key.(type) {
		case *rsa.PublicKey:
			if token.Method.Alg() != jwt.SigningMethodRS256.Alg() && token.Method.Alg() != jwt.SigningMethodRS384.Alg() && token.Method.Alg() != jwt.SigningMethodRS512.Alg() {
				return nil, errors.New("unexpected RSA assertion signing method")
			}
		case *ecdsa.PublicKey:
			if token.Method.Alg() != jwt.SigningMethodES256.Alg() && token.Method.Alg() != jwt.SigningMethodES384.Alg() && token.Method.Alg() != jwt.SigningMethodES512.Alg() {
				return nil, errors.New("unexpected ECDSA assertion signing method")
			}
		case ed25519.PublicKey:
			if token.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
				return nil, errors.New("unexpected EdDSA assertion signing method")
			}
		}
		return v.key, nil
	}, jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience), jwt.WithValidMethods([]string{
		jwt.SigningMethodRS256.Alg(), jwt.SigningMethodRS384.Alg(), jwt.SigningMethodRS512.Alg(),
		jwt.SigningMethodES256.Alg(), jwt.SigningMethodES384.Alg(), jwt.SigningMethodES512.Alg(),
		jwt.SigningMethodEdDSA.Alg(),
	}))
	if err != nil || token == nil || !token.Valid {
		if err == nil {
			err = errors.New("invalid assertion")
		}
		return nil, err
	}
	if strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.ID) == "" || strings.TrimSpace(claims.Nonce) == "" {
		return nil, errors.New("assertion sub, jti, and nonce are required")
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return nil, errors.New("assertion iat and exp are required")
	}
	now := time.Now()
	if claims.IssuedAt.Time.After(now.Add(60*time.Second)) || claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) > 5*time.Minute {
		return nil, errors.New("assertion lifetime is invalid")
	}
	if strings.TrimSpace(claims.EnterpriseID) == "" || strings.TrimSpace(claims.WorkspaceID) == "" {
		return nil, errors.New("assertion enterprise_id and workspace_id are required")
	}
	return claims, nil
}

type federatedTenantInput struct {
	Source               string `json:"source"`
	ExternalEnterpriseID string `json:"externalEnterpriseId"`
	ExternalWorkspaceID  string `json:"externalWorkspaceId"`
}

type federatedProjectInput struct {
	Source              string `json:"source"`
	ExternalWorkspaceID string `json:"externalWorkspaceId"`
	ExternalProjectID   string `json:"externalProjectId"`
	ExternalProjectName string `json:"externalProjectName"`
	ProjectID           string `json:"projectId"`
}

func decodeFederatedJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return false
	}
	return true
}

func normalizeFederatedSource(source string) string {
	if strings.TrimSpace(source) == "" {
		return federatedAssertionSource
	}
	return strings.ToLower(strings.TrimSpace(source))
}

func (h *Handler) BindFederatedTenant(w http.ResponseWriter, r *http.Request) {
	if h.federated == nil {
		databaseUnavailable(w)
		return
	}
	var input federatedTenantInput
	if !decodeFederatedJSON(w, r, &input) {
		return
	}
	input.Source = normalizeFederatedSource(input.Source)
	input.ExternalEnterpriseID = strings.TrimSpace(input.ExternalEnterpriseID)
	input.ExternalWorkspaceID = strings.TrimSpace(input.ExternalWorkspaceID)
	if input.Source != federatedAssertionSource || input.ExternalEnterpriseID == "" || input.ExternalWorkspaceID == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_FEDERATED_TENANT", "source、企业标识和 Workspace 标识均为必填")
		return
	}
	actor := identityFromContext(r.Context())
	createdBy := ""
	if actor != nil && actor.Principal != nil {
		createdBy = actor.Principal.ID
	}
	binding := &domain.ExternalTenantBinding{Source: input.Source, ExternalEnterpriseID: input.ExternalEnterpriseID, ExternalWorkspaceID: input.ExternalWorkspaceID, CreatedBy: createdBy}
	if err := h.federated.BindTenant(r.Context(), binding); err != nil {
		if h.federated.IsUniqueViolation(err) {
			jsonError(w, http.StatusConflict, "FEDERATED_TENANT_CONFLICT", "企业或 Workspace 已绑定其他 PAM Tenant")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存联邦企业绑定失败")
		return
	}
	if actor != nil && actor.Principal != nil {
		h.writeNotificationAudit(r.Context(), "federated.tenant_bound", binding.ID, "联邦企业已绑定 PAM Tenant", map[string]any{"source": binding.Source, "external_enterprise_id": binding.ExternalEnterpriseID, "external_workspace_id": binding.ExternalWorkspaceID}, actor.Principal.ID, string(actor.Principal.Kind))
	}
	jsonResp(w, http.StatusOK, binding)
}

func (h *Handler) ListFederatedTenants(w http.ResponseWriter, r *http.Request) {
	if h.federated == nil {
		databaseUnavailable(w)
		return
	}
	items, err := h.federated.ListTenants(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询联邦企业绑定失败")
		return
	}
	jsonResp(w, http.StatusOK, items)
}

func (h *Handler) BindFederatedProject(w http.ResponseWriter, r *http.Request) {
	if h.federated == nil {
		databaseUnavailable(w)
		return
	}
	var input federatedProjectInput
	if !decodeFederatedJSON(w, r, &input) {
		return
	}
	input.Source = normalizeFederatedSource(input.Source)
	input.ExternalWorkspaceID = strings.TrimSpace(input.ExternalWorkspaceID)
	input.ExternalProjectID = strings.TrimSpace(input.ExternalProjectID)
	input.ExternalProjectName = strings.TrimSpace(input.ExternalProjectName)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if input.Source != federatedAssertionSource || input.ExternalWorkspaceID == "" || input.ExternalProjectID == "" || input.ProjectID == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_FEDERATED_PROJECT", "source、Workspace、外部 Project 和 PAM Project 均为必填")
		return
	}
	actor := identityFromContext(r.Context())
	createdBy := ""
	if actor != nil && actor.Principal != nil {
		createdBy = actor.Principal.ID
	}
	binding := &domain.ExternalProjectBinding{Source: input.Source, ExternalWorkspaceID: input.ExternalWorkspaceID, ExternalProjectID: input.ExternalProjectID, ExternalProjectName: input.ExternalProjectName, ProjectID: input.ProjectID, CreatedBy: createdBy}
	if err := h.federated.BindProject(r.Context(), binding); err != nil {
		if h.federated.IsUniqueViolation(err) {
			jsonError(w, http.StatusConflict, "FEDERATED_PROJECT_CONFLICT", "外部 Project 已绑定其他 PAM Project")
			return
		}
		jsonError(w, http.StatusBadRequest, "INVALID_FEDERATED_PROJECT", "PAM Project 不存在或不属于当前 Tenant")
		return
	}
	if actor != nil && actor.Principal != nil {
		h.writeNotificationAudit(r.Context(), "federated.project_bound", binding.ID, "联邦 Project 已绑定 PAM Project", map[string]any{"source": binding.Source, "external_workspace_id": binding.ExternalWorkspaceID, "external_project_id": binding.ExternalProjectID, "project_id": binding.ProjectID}, actor.Principal.ID, string(actor.Principal.Kind))
	}
	jsonResp(w, http.StatusOK, binding)
}

func (h *Handler) ListFederatedProjects(w http.ResponseWriter, r *http.Request) {
	if h.federated == nil {
		databaseUnavailable(w)
		return
	}
	items, err := h.federated.ListProjects(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询联邦 Project 绑定失败")
		return
	}
	jsonResp(w, http.StatusOK, items)
}

func (h *Handler) RevokeFederatedProject(w http.ResponseWriter, r *http.Request) {
	if h.federated == nil {
		databaseUnavailable(w)
		return
	}
	ok, err := h.federated.RevokeProject(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销联邦 Project 绑定失败")
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "FEDERATED_PROJECT_NOT_FOUND", "联邦 Project 绑定不存在")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type federatedLoginRequest struct {
	Assertion string `json:"assertion"`
}

func (h *Handler) FederatedLogin(w http.ResponseWriter, r *http.Request) {
	if h.federated == nil || h.principals == nil {
		databaseUnavailable(w)
		return
	}
	if h.federatedAssertion == nil {
		jsonError(w, http.StatusServiceUnavailable, "FEDERATED_AUTH_NOT_CONFIGURED", "联邦授权尚未配置")
		return
	}
	var input federatedLoginRequest
	if !decodeFederatedJSON(w, r, &input) || strings.TrimSpace(input.Assertion) == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_ASSERTION", "assertion 为必填")
		return
	}
	claims, err := h.federatedAssertion.verify(strings.TrimSpace(input.Assertion))
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "INVALID_ASSERTION", "联邦授权断言无效")
		return
	}
	if err := h.federated.ConsumeAssertion(r.Context(), claims.Issuer, claims.ID, claims.ExpiresAt.Time); err != nil {
		if errors.Is(err, postgres.ErrFederatedAssertionReplay) {
			jsonError(w, http.StatusUnauthorized, "ASSERTION_REPLAYED", "联邦授权断言已使用")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存授权断言状态失败")
		return
	}
	binding, err := h.federated.ResolveTenant(r.Context(), federatedAssertionSource, claims.EnterpriseID, claims.WorkspaceID)
	if err != nil {
		if errors.Is(err, postgres.ErrFederatedTenantNotFound) {
			jsonError(w, http.StatusForbidden, "FEDERATED_TENANT_NOT_BOUND", "联邦企业尚未绑定 PAM Tenant")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询联邦企业绑定失败")
		return
	}
	ctx := domain.WithTenantID(r.Context(), binding.TenantID)
	metadata := map[string]any{
		"federated": true, "issuer": claims.Issuer, "subject": claims.Subject,
		"source": federatedAssertionSource, "workspace_id": claims.WorkspaceID,
		"workspace_role": strings.TrimSpace(claims.WorkspaceRole),
	}
	if claims.Email != "" {
		metadata["email"] = claims.Email
	}
	if claims.DisplayName != "" {
		metadata["display_name"] = claims.DisplayName
	}
	principal, err := h.federated.ProvisionIdentity(ctx, claims.Issuer, claims.Subject, claims.Email, claims.DisplayName, binding.TenantID, metadata)
	if err != nil {
		if errors.Is(err, postgres.ErrFederatedIdentityTenant) {
			jsonError(w, http.StatusForbidden, "FEDERATED_IDENTITY_TENANT_MISMATCH", "联邦用户已绑定其他 PAM Tenant")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "Provision PAM 用户失败")
		return
	}
	var projectBinding *domain.ExternalProjectBinding
	if claims.ProjectID != "" {
		projectBinding, err = h.federated.ResolveProject(ctx, federatedAssertionSource, claims.WorkspaceID, claims.ProjectID)
		if err != nil {
			if errors.Is(err, postgres.ErrExternalProjectBindingNotFound) {
				jsonResp(w, http.StatusConflict, map[string]any{"code": "PROJECT_BINDING_REQUIRED", "message": "联邦 Project 尚未绑定 PAM Project", "externalProjectId": claims.ProjectID, "workspaceId": claims.WorkspaceID, "principal": principal})
				return
			}
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询联邦 Project 绑定失败")
			return
		}
	}
	token, expiresAt, err := h.issueJWTWithTTL(principal, 15*time.Minute)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "TOKEN_ISSUE_FAILED", "签发 PAM 访问令牌失败")
		return
	}
	projects := []domain.Project{}
	if h.projectMemberships != nil {
		if items, listErr := h.projectMemberships.ListForPrincipal(ctx, principal.ID); listErr == nil && items != nil {
			projects = items
		}
	}
	if h.audit != nil {
		h.writeNotificationAudit(ctx, "federated.login", principal.ID, "联邦用户登录 PAM", map[string]any{"issuer": claims.Issuer, "subject": claims.Subject, "workspace_id": claims.WorkspaceID, "project_id": claims.ProjectID}, principal.ID, string(principal.Kind))
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"accessToken":    token,
		"tokenType":      "Bearer",
		"expiresAt":      expiresAt,
		"user":           principal,
		"projects":       projects,
		"projectBinding": projectBinding,
	})
}
