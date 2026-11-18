package control

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

// RemoveAccountFromOrganization is the platform-side "person left / was removed"
// action, shown on the accounts page for every business-organization member.
// It branches by identity source because disabling behaves differently:
//
//   - SSO / federated members: moved back to the unaffiliated sentinel so their
//     next SSO login lands in the "no organization yet" hall with no business
//     scope. Simply disabling them is useless because ProvisionIdentity re-activates
//     them on the next IdP login.
//   - local password members: disabled (DISABLED) — a local account belongs to
//     exactly one tenant, so leaving it in place but inactive is the correct,
//     auditable way to revoke access.
//
// Either way the account's live sessions (and any API keys for local accounts)
// are revoked immediately. Guardrails: cannot target the built-in super admin or
// yourself, and cannot remove the last tenant administrator of an organization.
func (h *Handler) RemoveAccountFromOrganization(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	targetID := strings.TrimSpace(r.PathValue("id"))
	if targetID == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "缺少账号标识")
		return
	}
	if targetID == adminPrincipalID {
		jsonError(w, http.StatusForbidden, "BUILTIN_ADMIN_PROTECTED", "内置超级管理员不可移出")
		return
	}
	identity := identityFromContext(r.Context())
	if identity != nil && identity.Principal != nil && identity.Principal.ID == targetID {
		jsonError(w, http.StatusConflict, "CANNOT_REMOVE_SELF", "不能移除当前登录账号")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID, role, name, displayName string
	var federated bool
	err = tx.QueryRow(ctx, `
		SELECT p.tenant_id, COALESCE(NULLIF(p.metadata->>'role',''),'user'), p.name,
			COALESCE(NULLIF(p.metadata->>'display_name',''),''),
			EXISTS (SELECT 1 FROM federated_identities f WHERE f.principal_id = p.id)
		FROM principals p WHERE p.id::text=$1 AND p.kind='user' FOR UPDATE OF p`, targetID).
		Scan(&tenantID, &role, &name, &displayName, &federated)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询账号失败")
		return
	}
	if tenantID == unaffiliatedTenant {
		jsonError(w, http.StatusConflict, "ALREADY_UNAFFILIATED", "该账号尚未归属任何组织")
		return
	}
	if role == RoleTenantAdmin && tenantID != platformTenantSlug {
		// The platform management domain always retains the built-in super
		// administrator, so the last-admin guard only applies to business orgs.
		var otherAdmins int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM principals
			WHERE kind='user' AND status='ACTIVE' AND tenant_id=$2 AND metadata->>'role'='tenant_admin'
			  AND id::text <> $1`, targetID, tenantID).Scan(&otherAdmins); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "校验管理员数量失败")
			return
		}
		if otherAdmins == 0 {
			jsonError(w, http.StatusConflict, "LAST_TENANT_ADMIN", "每个组织至少保留一个管理员，无法移出该账号")
			return
		}
	}

	action := "removed"
	revoked := 0
	if federated {
		// SSO member → back to the unaffiliated hall (role normalized to user).
		if _, _, revoked, err = movePrincipalBetweenTenantsTx(ctx, tx, targetID, tenantID, unaffiliatedTenant, RoleUser); err != nil {
			if errors.Is(err, errUnaffiliatedPrincipalGone) {
				jsonError(w, http.StatusConflict, "ALREADY_UNAFFILIATED", "该账号状态已变更，请刷新")
				return
			}
			if errors.Is(err, errNameTakenInTenant) {
				jsonError(w, http.StatusConflict, "NAME_TAKEN_IN_TENANT", "未分配列表中已存在同名身份")
				return
			}
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "移出组织失败")
			return
		}
	} else {
		// Local member → disable in place and revoke all credentials.
		if _, err := tx.Exec(ctx, `
			UPDATE principals SET status='DISABLED', updated_at=now()
			WHERE id::text=$1 AND tenant_id=$2 AND kind='user'`, targetID, tenantID); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "停用账号失败")
			return
		}
		if _, err := tx.Exec(ctx, `
			UPDATE principal_api_keys SET status='REVOKED', revoked_at=now()
			WHERE principal_id=$1::uuid AND status='ACTIVE'`, targetID); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销凭据失败")
			return
		}
		tag, err := tx.Exec(ctx, `
			UPDATE admin_sessions SET revoked_at=now() WHERE principal_id::text=$1 AND revoked_at IS NULL`, targetID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销会话失败")
			return
		}
		revoked = int(tag.RowsAffected())
		action = "disabled"
	}

	label := displayName
	if label == "" {
		label = name
	}
	if h.audit != nil {
		actorID := ""
		if identity != nil && identity.Principal != nil {
			actorID = identity.Principal.ID
		}
		metadata := map[string]any{
			"action": action, "previous_tenant": tenantID, "role": role,
			"revoked_sessions": revoked, "target_name": name,
		}
		auditTo := tenantID
		if action == "removed" {
			auditTo = unaffiliatedTenant
		}
		metadata["new_tenant"] = auditTo
		event := &domain.AuditEvent{
			Action: "account.removed", ActorID: actorID, ActorType: "user",
			TargetID: targetID, Summary: "成员已从组织移出: " + label, Metadata: metadata,
		}
		if err := h.audit.CreateTx(domain.WithTenantID(ctx, tenantID), tx, event); err != nil {
			log.Printf("member removal audit failed: %v", err)
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "写入审计失败")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "提交失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"id": targetID, "action": action, "previous_tenant": tenantID, "revoked_sessions": revoked,
	})
}

// ReenableAccount restores a disabled local password member to ACTIVE. It is
// the inverse of the local branch of RemoveAccountFromOrganization, available
// to the super admin on the accounts page (tenant admins use the project-users
// toggle for their own organization). Federated accounts are rejected: they
// re-activate automatically on the next IdP login, and a removed SSO member
// must be re-assigned from the unaffiliated list instead.
func (h *Handler) ReenableAccount(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	targetID := strings.TrimSpace(r.PathValue("id"))
	if targetID == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "缺少账号标识")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID, status, name, displayName string
	var federated bool
	err = tx.QueryRow(ctx, `
		SELECT p.tenant_id, p.status, p.name,
			COALESCE(NULLIF(p.metadata->>'display_name',''),''),
			EXISTS (SELECT 1 FROM federated_identities f WHERE f.principal_id = p.id)
		FROM principals p WHERE p.id::text=$1 AND p.kind='user' FOR UPDATE OF p`, targetID).
		Scan(&tenantID, &status, &name, &displayName, &federated)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询账号失败")
		return
	}
	if federated {
		jsonError(w, http.StatusConflict, "FEDERATED_AUTO_REACTIVATES", "SSO 账号无需手动启用：下次身份提供方登录会自动生效；若已移出组织，请在未分配列表重新指派")
		return
	}
	if status != string(domain.PrincipalStatusDisabled) {
		jsonError(w, http.StatusConflict, "ALREADY_ACTIVE", "该账号当前不是停用状态")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE principals SET status='ACTIVE', updated_at=now()
		WHERE id::text=$1 AND tenant_id=$2 AND kind='user'`, targetID, tenantID); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "恢复账号失败")
		return
	}
	label := displayName
	if label == "" {
		label = name
	}
	if h.audit != nil {
		actorID := ""
		if identity := identityFromContext(ctx); identity != nil && identity.Principal != nil {
			actorID = identity.Principal.ID
		}
		event := &domain.AuditEvent{
			Action: "account.reenabled", ActorID: actorID, ActorType: "user",
			TargetID: targetID, Summary: "本地账号已恢复启用: " + label,
			Metadata: map[string]any{"tenant_id": tenantID, "target_name": name},
		}
		if err := h.audit.CreateTx(domain.WithTenantID(ctx, tenantID), tx, event); err != nil {
			log.Printf("account reenable audit failed: %v", err)
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "写入审计失败")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "提交失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"id": targetID, "status": "ACTIVE"})
}
