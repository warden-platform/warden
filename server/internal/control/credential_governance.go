package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

// executeCredentialAction performs a control-plane inline credential action.
// Unlike Connector-dispatched actions it never claims a task and never creates
// an AccessGrant: the policy decision, approval, audit and timeout pipeline is
// still honored, but the effect is applied directly against the Warden-managed
// credential ledger.
//
// Plaintext boundary: secretValue exists only in memory. It is AES-GCM
// encrypted before persistence; responses and audit metadata carry only
// valuePreview and value_sha256.
func (h *Handler) executeCredentialAction(ctx context.Context, tx pgx.Tx, exec *domain.Execution, action *domain.ActionDefinition, res *domain.Resource, secretValue string) (map[string]any, error) {
	actionCtx, cancel := context.WithTimeout(ctx, time.Duration(action.TimeoutSeconds)*time.Second)
	defer cancel()
	switch action.Name {
	case "credential.create":
		return h.credentialCreate(actionCtx, tx, exec, res, secretValue)
	case "credential.rotate":
		return h.credentialRotate(actionCtx, tx, exec, res, secretValue)
	case "credential.revoke":
		return h.credentialRevoke(actionCtx, tx, exec, res)
	case "credential.bind":
		return h.credentialBind(actionCtx, tx, exec, res, true)
	case "credential.unbind":
		return h.credentialBind(actionCtx, tx, exec, res, false)
	case "credential.get":
		return h.credentialGet(actionCtx, tx, exec, res)
	case "credential.list":
		return h.credentialList(actionCtx, tx, exec)
	default:
		return nil, fmt.Errorf("unsupported control-plane action %s", action.Name)
	}
}

func (h *Handler) credentialCreate(ctx context.Context, tx pgx.Tx, exec *domain.Execution, res *domain.Resource, secretValue string) (map[string]any, error) {
	name, _ := exec.Parameters["name"].(string)
	kind, _ := exec.Parameters["kind"].(string)
	username, _ := exec.Parameters["username"].(string)
	purpose, _ := exec.Parameters["purpose"].(string)
	if !domain.ValidateCredentialName(name) {
		return nil, fmt.Errorf("credential name %q is invalid", name)
	}
	existing, err := h.credentials.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("credential %q already exists", name)
	}
	encrypted, err := h.credentialCipher.Encrypt([]byte(secretValue))
	if err != nil {
		return nil, fmt.Errorf("encrypt credential value: %w", err)
	}
	expiresAt := expiresAtFromParameters(exec.Parameters)
	cred := &domain.Credential{
		Name: name, Kind: kind, Username: username,
		EncryptedValue: encrypted, ValuePreview: domain.MaskSecretValue(secretValue),
		Purpose: purpose, Scope: map[string]any{"bindings": []any{}},
		Status: domain.CredentialActive, Version: 1, ExpiresAt: expiresAt,
		CreatedBy: exec.PrincipalID,
	}
	if err := h.credentials.CreateTx(ctx, tx, cred); err != nil {
		return nil, err
	}
	catalogRes, err := h.syncCredentialCatalogResource(ctx, tx, cred)
	if err != nil {
		return nil, err
	}
	event := credentialAuditEvent("credential.created", exec, cred, map[string]any{
		"name": name, "kind": kind, "version": cred.Version, "value_preview": cred.ValuePreview,
		"resource_id": catalogRes.ID, "value_sha256": domain.HashValue(secretValue),
	})
	if err := h.audit.CreateTx(ctx, tx, event); err != nil {
		return nil, err
	}
	return map[string]any{
		"name": name, "kind": kind, "status": cred.Status, "version": cred.Version,
		"valuePreview": cred.ValuePreview, "expiresAt": cred.ExpiresAt, "resourceId": catalogRes.ID,
	}, nil
}

func (h *Handler) credentialRotate(ctx context.Context, tx pgx.Tx, exec *domain.Execution, res *domain.Resource, secretValue string) (map[string]any, error) {
	cred, err := h.credentials.GetByName(ctx, res.Name)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credential %q not found", res.Name)
	}
	if cred.Status != domain.CredentialActive {
		return nil, fmt.Errorf("credential %q is not ACTIVE", res.Name)
	}
	encrypted, err := h.credentialCipher.Encrypt([]byte(secretValue))
	if err != nil {
		return nil, fmt.Errorf("encrypt credential value: %w", err)
	}
	version, err := h.credentials.RotateTx(ctx, tx, cred.ID, encrypted, domain.MaskSecretValue(secretValue))
	if err != nil {
		return nil, err
	}
	cred.Version = version
	cred.ValuePreview = domain.MaskSecretValue(secretValue)
	if expiresAt := expiresAtFromParameters(exec.Parameters); expiresAt != nil {
		cred.ExpiresAt = expiresAt
		if _, err := tx.Exec(ctx, `UPDATE credentials SET expires_at=$2, updated_at=now() WHERE id=$1 AND tenant_id=$3`, cred.ID, expiresAt, domain.TenantIDFromContext(ctx)); err != nil {
			return nil, err
		}
	}
	if _, err := h.syncCredentialCatalogResource(ctx, tx, cred); err != nil {
		return nil, err
	}
	rotatedAt := time.Now()
	event := credentialAuditEvent("credential.rotated", exec, cred, map[string]any{
		"name": res.Name, "version": cred.Version, "value_preview": cred.ValuePreview,
		"value_sha256": domain.HashValue(secretValue),
	})
	if err := h.audit.CreateTx(ctx, tx, event); err != nil {
		return nil, err
	}
	return map[string]any{
		"name": res.Name, "status": cred.Status, "version": cred.Version,
		"valuePreview": cred.ValuePreview, "rotatedAt": rotatedAt,
	}, nil
}

func (h *Handler) credentialRevoke(ctx context.Context, tx pgx.Tx, exec *domain.Execution, res *domain.Resource) (map[string]any, error) {
	confirmName, _ := exec.Parameters["confirmName"].(string)
	if confirmName != res.Name {
		return nil, fmt.Errorf("revoke confirmation must match credential name exactly")
	}
	cred, err := h.credentials.GetByName(ctx, res.Name)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credential %q not found", res.Name)
	}
	if cred.Status != domain.CredentialActive {
		return nil, fmt.Errorf("credential %q is not ACTIVE", res.Name)
	}
	revoked, err := h.credentials.RevokeTx(ctx, tx, cred.ID, confirmName)
	if err != nil {
		return nil, err
	}
	if !revoked {
		return nil, fmt.Errorf("credential %q is not ACTIVE", res.Name)
	}
	now := time.Now()
	cred.Status = domain.CredentialRevoked
	cred.RevokedAt = &now
	if _, err := h.syncCredentialCatalogResource(ctx, tx, cred); err != nil {
		return nil, err
	}
	event := credentialAuditEvent("credential.revoked", exec, cred, map[string]any{"name": res.Name, "version": cred.Version})
	if err := h.audit.CreateTx(ctx, tx, event); err != nil {
		return nil, err
	}
	return map[string]any{"name": res.Name, "status": domain.CredentialRevoked, "revokedAt": cred.RevokedAt}, nil
}

func (h *Handler) credentialBind(ctx context.Context, tx pgx.Tx, exec *domain.Execution, res *domain.Resource, bind bool) (map[string]any, error) {
	cred, err := h.credentials.GetByName(ctx, res.Name)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credential %q not found", res.Name)
	}
	if cred.Status != domain.CredentialActive {
		return nil, fmt.Errorf("credential %q is not ACTIVE", res.Name)
	}
	targetType, _ := exec.Parameters["targetType"].(string)
	targetName, _ := exec.Parameters["targetName"].(string)
	bindings := cred.CredentialBindings()
	filtered := make([]domain.CredentialBinding, 0, len(bindings)+1)
	found := false
	for _, entry := range bindings {
		if entry.TargetType == targetType && entry.TargetName == targetName {
			found = true
			if bind {
				filtered = append(filtered, entry)
			}
			continue
		}
		filtered = append(filtered, entry)
	}
	if bind && !found {
		filtered = append(filtered, domain.CredentialBinding{TargetType: targetType, TargetName: targetName})
	}
	if !bind && !found {
		return nil, fmt.Errorf("credential %q is not bound to %s/%s", res.Name, targetType, targetName)
	}
	scope := map[string]any{"bindings": bindingsToAny(filtered)}
	if err := h.credentials.SetScopeTx(ctx, tx, cred.ID, scope); err != nil {
		return nil, err
	}
	cred.Scope = scope
	auditAction := "credential.bound"
	if !bind {
		auditAction = "credential.unbound"
	}
	event := credentialAuditEvent(auditAction, exec, cred, map[string]any{
		"name": res.Name, "target_type": targetType, "target_name": targetName,
	})
	if err := h.audit.CreateTx(ctx, tx, event); err != nil {
		return nil, err
	}
	return map[string]any{"name": res.Name, "bindings": filtered}, nil
}

func (h *Handler) credentialGet(ctx context.Context, tx pgx.Tx, exec *domain.Execution, res *domain.Resource) (map[string]any, error) {
	cred, err := h.credentials.GetByName(ctx, res.Name)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credential %q not found", res.Name)
	}
	event := credentialAuditEvent("credential.get", exec, cred, map[string]any{
		"name": cred.Name, "version": cred.Version, "status": cred.Status, "value_preview": cred.ValuePreview,
	})
	if err := h.audit.CreateTx(ctx, tx, event); err != nil {
		return nil, err
	}
	return map[string]any{
		"name": cred.Name, "kind": cred.Kind, "status": cred.Status, "version": cred.Version,
		"valuePreview": cred.ValuePreview, "expiresAt": cred.ExpiresAt, "lastUsedAt": cred.LastUsedAt,
		"purpose": cred.Purpose, "bindings": cred.CredentialBindings(),
	}, nil
}

func (h *Handler) credentialList(ctx context.Context, tx pgx.Tx, exec *domain.Execution) (map[string]any, error) {
	creds, err := h.credentials.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(creds))
	for _, c := range creds {
		items = append(items, map[string]any{
			"name": c.Name, "kind": c.Kind, "status": c.Status, "version": c.Version,
			"valuePreview": c.ValuePreview, "expiresAt": c.ExpiresAt, "lastUsedAt": c.LastUsedAt,
		})
	}
	event := credentialAuditEvent("credential.list", exec, nil, map[string]any{"count": len(creds)})
	if err := h.audit.CreateTx(ctx, tx, event); err != nil {
		return nil, err
	}
	return map[string]any{"credentials": items}, nil
}

// ResolveCredentialValue lets an authenticated Connector fetch the plaintext
// of a "warden:" credential reference for an integration bound to that
// credential. It is a runtime resolution, not an Agent action: no execution,
// approval or grant is involved, and the plaintext is only ever sent over TLS
// to the requesting Connector. The credential's binding scope is enforced:
// with a non-empty bindings list the request must name an integration that is
// both bound to the credential and owned by the requesting Connector.
func (h *Handler) ResolveCredentialValue(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接")
		return
	}
	var req struct {
		ConnectorID   string `json:"connectorId"`
		IntegrationID string `json:"integrationId"`
		Name          string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	ctx := r.Context()
	connector, connectorErr := h.connectors.GetByID(ctx, strings.TrimSpace(req.ConnectorID))
	if connectorErr != nil || connector == nil || connector.Status != domain.ConnectorActive {
		jsonError(w, http.StatusForbidden, "CONNECTOR_NOT_ACTIVE", "Connector 不存在或未激活")
		return
	}
	cred, err := h.credentials.GetSecretByName(ctx, req.Name)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询凭据失败")
		return
	}
	if cred == nil {
		jsonError(w, http.StatusNotFound, "CREDENTIAL_NOT_FOUND", "凭据不存在")
		return
	}
	if cred.Status != domain.CredentialActive {
		jsonError(w, http.StatusConflict, "CREDENTIAL_NOT_ACTIVE", "凭据未激活")
		return
	}
	if cred.ExpiresAt != nil && cred.ExpiresAt.Before(time.Now()) {
		jsonError(w, http.StatusConflict, "CREDENTIAL_EXPIRED", "凭据已过期")
		return
	}
	if cred.TenantID != connector.TenantID {
		jsonError(w, http.StatusForbidden, "CREDENTIAL_TENANT_MISMATCH", "凭据与 Connector 不属于同一租户")
		return
	}
	if bindings := cred.CredentialBindings(); len(bindings) > 0 {
		if req.IntegrationID == "" {
			jsonError(w, http.StatusForbidden, "CREDENTIAL_BINDING_REQUIRED", "凭据已绑定特定集成，缺少 integrationId")
			return
		}
		ig, igErr := h.integrations.GetByID(ctx, req.IntegrationID)
		if igErr != nil || ig == nil {
			jsonError(w, http.StatusForbidden, "CREDENTIAL_NOT_BOUND", "凭据未绑定到该集成")
			return
		}
		// credential.bind stores the integration by its display name; tolerate
		// either the UUID or the name so bindings survive integration renames.
		bound := false
		for _, b := range bindings {
			if b.TargetType == "integration" &&
				(b.TargetName == req.IntegrationID || b.TargetName == ig.Name) {
				bound = true
				break
			}
		}
		if !bound {
			jsonError(w, http.StatusForbidden, "CREDENTIAL_NOT_BOUND", "凭据未绑定到该集成")
			return
		}
		if ig.ConnectorID != req.ConnectorID {
			jsonError(w, http.StatusForbidden, "CREDENTIAL_CONNECTOR_MISMATCH", "凭据绑定集成不属于该执行通道")
			return
		}
		if ig.TenantID != connector.TenantID {
			jsonError(w, http.StatusForbidden, "CREDENTIAL_TENANT_MISMATCH", "凭据与 Connector 不属于同一租户")
			return
		}
	}
	value, err := h.credentialCipher.Decrypt(cred.EncryptedValue)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "CREDENTIAL_DECRYPT_FAILED", "凭据解密失败")
		return
	}
	if err := h.credentials.MarkUsed(ctx, cred.ID); err != nil {
		log.Printf("mark credential %s used: %v", cred.Name, err)
	}
	// Audit the reference resolution without any plaintext.
	event := &domain.AuditEvent{
		Action: "credential.used", ActorType: "connector", TargetID: cred.Name, Summary: "credential.used",
		Metadata: map[string]any{"name": cred.Name, "version": cred.Version,
			"connector_id": req.ConnectorID, "integration_id": req.IntegrationID},
	}
	if err := h.audit.Create(ctx, event); err != nil {
		log.Printf("write credential.used audit: %v", err)
	}
	jsonResp(w, http.StatusOK, map[string]any{"value": string(value)})
}

// credentialAuditEvent builds an audit event attributed to the execution's
// authenticated principal. Metadata never contains plaintext.
func credentialAuditEvent(action string, exec *domain.Execution, credential *domain.Credential, metadata map[string]any) *domain.AuditEvent {
	event := &domain.AuditEvent{
		Action: action, ActorID: exec.PrincipalID, ActorType: "user",
		ResourceID: exec.ResourceID, ExecutionID: exec.ID, TraceID: exec.TraceID,
		Summary: action, Metadata: map[string]any{},
	}
	if credential != nil {
		event.TargetID = credential.Name
	}
	for key, value := range metadata {
		event.Metadata[key] = value
	}
	attachExecutionAudit(event, exec)
	return event
}

// syncCredentialCatalogResource registers or refreshes the "credential"
// catalog resource that lets Agents select a credential in the execution
// directory and lets the Web show credential lifecycle state.
func (h *Handler) syncCredentialCatalogResource(ctx context.Context, tx pgx.Tx, c *domain.Credential) (*domain.Resource, error) {
	resource := &domain.Resource{
		ResourceType: "credential", ExternalID: "warden/credential/" + c.Name, Name: c.Name,
		ManagementType: domain.ManagementControlPlane, Environment: "testing", Status: domain.ResourceActive,
		Labels: map[string]any{}, Annotations: map[string]any{}, Metadata: credentialCatalogMetadata(c),
	}
	if err := h.resources.UpsertTx(ctx, tx, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

func credentialCatalogMetadata(c *domain.Credential) map[string]any {
	meta := map[string]any{
		"kind": c.Kind, "username": c.Username, "preview": c.ValuePreview,
		"version": c.Version, "status": c.Status, "managedBy": "control_plane",
	}
	if c.ExpiresAt != nil {
		meta["expires"] = c.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return meta
}

func expiresAtFromParameters(parameters map[string]any) *time.Time {
	seconds, ok := parameters["expiresInSeconds"].(float64)
	if !ok || seconds <= 0 {
		return nil
	}
	expiresAt := time.Now().Add(time.Duration(seconds) * time.Second)
	return &expiresAt
}

func bindingsToAny(bindings []domain.CredentialBinding) []any {
	out := make([]any, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, map[string]any{"target_type": binding.TargetType, "target_name": binding.TargetName})
	}
	return out
}
