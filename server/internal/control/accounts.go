package control

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

// validateRoleChange keeps the guard rails testable without a database.
// The built-in break-glass admin is the single immutable super administrator;
// platform_admin can never be granted to anyone else. Tenant sub-admins are
// protected against self-demotion and against leaving a tenant with no admin.
func validateRoleChange(targetID, actorID, currentRole, newRole string, otherTenantAdmins int) (string, bool) {
	if !validAssignableRole(newRole) {
		return "INVALID_ACCOUNT_ROLE", false
	}
	if targetID == adminPrincipalID {
		return "BUILTIN_ADMIN_PROTECTED", false
	}
	if currentRole == RoleTenantAdmin && newRole != RoleTenantAdmin {
		if actorID == targetID {
			return "CANNOT_DEMOTE_SELF", false
		}
		if otherTenantAdmins == 0 {
			return "LAST_TENANT_ADMIN", false
		}
	}
	return "", true
}

// Account is the administrator-facing view of a login identity. Federated
// principals keep an opaque internal name; human-readable fields come from
// the IdP profile stored in principals.metadata / federated_identities.
type Account struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	DisplayName     string     `json:"display_name"`
	Email           string     `json:"email"`
	AvatarURL       string     `json:"avatar_url"`
	AuthSource      string     `json:"auth_source"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	TenantID        string     `json:"tenant_id"`
	TenantName      string     `json:"tenant_name"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	CreatedAt       time.Time  `json:"created_at"`
	IsBuiltinAdmin  bool       `json:"is_builtin_admin"`
	IsSelf          bool       `json:"is_self"`
	ProviderIssuer  string     `json:"provider_issuer,omitempty"`
	ProviderSubject string     `json:"provider_subject,omitempty"`
}

func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	if h.adminPool == nil {
		databaseUnavailable(w)
		return
	}
	actorID := ""
	actorTenant := platformTenantSlug
	superAdmin := false
	if identity := identityFromContext(r.Context()); identity != nil {
		if identity.Principal != nil {
			actorID = identity.Principal.ID
		}
		if identity.TenantID != "" {
			actorTenant = identity.TenantID
		}
		superAdmin = identity.HasScope(ScopeAdmin)
	}
	// The single super administrator sees every tenant's accounts; everyone
	// else (tenant sub-admins) stays scoped to their own tenant.
	tenantFilter := "AND p.tenant_id=$3"
	queryArgs := []any{adminPrincipalID, actorID, actorTenant}
	if superAdmin {
		tenantFilter = ""
		queryArgs = []any{adminPrincipalID, actorID}
	}
	// Auth source is derived from the actual federated binding, never from
	// self-declared metadata: it must match the branch RemoveAccountFromOrganization
	// takes (federated -> unaffiliated hall, local -> disable), so the UI badge
	// and the removal confirmation never promise a different outcome.
	rows, err := h.adminPool.Query(r.Context(), `
		SELECT p.id::text, p.name, p.status, p.tenant_id,
		       COALESCE(NULLIF(p.metadata->>'display_name',''), NULLIF(f.display_name,''), ''),
		       COALESCE(NULLIF(p.metadata->>'email',''), NULLIF(f.email,''), ''),
		       COALESCE(NULLIF(p.metadata->>'avatar_url',''), ''),
		       CASE WHEN f.id IS NOT NULL THEN 'oidc' ELSE 'local' END,
		       CASE WHEN p.id::text=$1 THEN 'platform_admin' ELSE COALESCE(NULLIF(p.metadata->>'role',''),'user') END,
		       p.last_login_at, p.created_at,
		       p.id::text=$1, p.id::text=$2,
		       COALESCE(f.issuer,''), COALESCE(f.subject,''),
		       COALESCE(t.name, p.tenant_id)
		FROM principals p
		LEFT JOIN LATERAL (
			SELECT id, issuer, subject, email, display_name
			FROM federated_identities WHERE principal_id=p.id
			ORDER BY last_login_at DESC NULLS LAST LIMIT 1
		) f ON TRUE
		LEFT JOIN tenants t ON t.slug=p.tenant_id
		WHERE p.kind='user' `+tenantFilter+`
		ORDER BY p.tenant_id, (p.id::text=$1) DESC, p.created_at DESC`, queryArgs...)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询账号列表失败")
		return
	}
	defer rows.Close()
	accounts := []Account{}
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Status, &a.TenantID, &a.DisplayName, &a.Email,
			&a.AvatarURL, &a.AuthSource, &a.Role, &a.LastLoginAt, &a.CreatedAt,
			&a.IsBuiltinAdmin, &a.IsSelf, &a.ProviderIssuer, &a.ProviderSubject, &a.TenantName); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取账号信息失败")
			return
		}
		// An unaffiliated federated identity has no tenant row, so the SQL
		// COALESCE would surface the raw sentinel. Render a human label and keep
		// the sentinel in TenantID so the console can offer the assign action.
		if a.TenantID == unaffiliatedTenant {
			a.TenantName = "未分配组织"
			a.Role = RoleUser
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取账号列表失败")
		return
	}
	jsonResp(w, http.StatusOK, accounts)
}

func (h *Handler) UpdateAccountRole(w http.ResponseWriter, r *http.Request) {
	if h.adminPool == nil {
		databaseUnavailable(w)
		return
	}
	targetID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	newRole := strings.TrimSpace(req.Role)
	actorID := ""
	actorType := "user"
	actorTenant := platformTenantSlug
	superAdmin := false
	if identity := identityFromContext(r.Context()); identity != nil {
		if identity.Principal != nil {
			actorID = identity.Principal.ID
			actorType = string(identity.Principal.Kind)
		}
		if identity.TenantID != "" {
			actorTenant = identity.TenantID
		}
		superAdmin = identity.HasScope(ScopeAdmin)
	}

	ctx := r.Context()
	tx, err := h.adminPool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The super administrator may manage accounts in any tenant; a tenant
	// sub-admin is confined to their own tenant. The target's tenant is read
	// from the row so cross-tenant changes audit against the right tenant.
	var currentRole, name, displayName, targetTenant string
	lookup := `
		SELECT COALESCE(NULLIF(metadata->>'role',''),'user'), name,
		       COALESCE(NULLIF(metadata->>'display_name',''), ''), tenant_id
		FROM principals
		WHERE id::text=$1 AND kind='user'`
	args := []any{targetID}
	if !superAdmin {
		lookup += ` AND tenant_id=$2`
		args = append(args, actorTenant)
	}
	lookup += ` FOR UPDATE`
	err = tx.QueryRow(ctx, lookup, args...).Scan(&currentRole, &name, &displayName, &targetTenant)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询账号失败")
		return
	}

	var otherTenantAdmins int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM principals
		WHERE kind='user' AND metadata->>'role'='tenant_admin' AND tenant_id=$3
		  AND id::text <> $1 AND id::text <> $2`, targetID, adminPrincipalID, targetTenant).Scan(&otherTenantAdmins); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "校验管理员数量失败")
		return
	}
	if code, ok := validateRoleChange(targetID, actorID, currentRole, newRole, otherTenantAdmins); !ok {
		messages := map[string]string{
			"INVALID_ACCOUNT_ROLE":    "角色只能是 user 或 tenant_admin；超级管理员仅内置 admin 一个，不可指派",
			"BUILTIN_ADMIN_PROTECTED": "内置超级管理员账号不可修改（故障应急入口）",
			"CANNOT_DEMOTE_SELF":      "不能给自己降权，请由其他管理员操作",
			"LAST_TENANT_ADMIN":       "每个租户至少保留一个租户管理员",
		}
		status := http.StatusBadRequest
		if code != "INVALID_ACCOUNT_ROLE" {
			status = http.StatusConflict
		}
		jsonError(w, status, code, messages[code])
		return
	}

	if _, err := tx.Exec(ctx, `
		UPDATE principals SET metadata = metadata || jsonb_build_object('role',$2::text), updated_at=now()
		WHERE id::text=$1 AND tenant_id=$3`, targetID, newRole, targetTenant); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新角色失败")
		return
	}
	// Revoke live sessions so a demotion takes effect immediately instead of
	// waiting for the old JWT (and its warden:admin scope) to expire.
	tag, err := tx.Exec(ctx, `
		UPDATE admin_sessions SET revoked_at=now()
		WHERE principal_id::text=$1 AND revoked_at IS NULL`, targetID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销会话失败")
		return
	}
	revoked := int(tag.RowsAffected())

	// The audit entry shares the role-change transaction: if the ledger write
	// fails, the role change is rolled back instead of silently going unlogged.
	label := displayName
	if label == "" {
		label = name
	}
	if h.audit != nil {
		metadata := map[string]any{
			"previous_role": currentRole, "new_role": newRole,
			"revoked_sessions": revoked, "target_name": name, "target_tenant": targetTenant,
		}
		event := &domain.AuditEvent{
			Action: "account.role_updated", ActorID: actorID, ActorType: actorType,
			TargetID: targetID, Summary: "账号角色已变更: " + label, Metadata: metadata,
		}
		if identity := identityFromContext(ctx); identity != nil {
			event.CredentialID = identity.CredentialID
			for key, value := range identity.AuditMetadata {
				metadata[key] = value
			}
		}
		auditCtx := domain.WithTenantID(ctx, targetTenant)
		if err := h.audit.CreateTx(auditCtx, tx, event); err != nil {
			log.Printf("account role audit failed: %v", err)
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "写入角色变更审计失败")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "提交角色变更失败")
		return
	}

	jsonResp(w, http.StatusOK, map[string]any{
		"id": targetID, "role": newRole, "previous_role": currentRole,
		"revoked_sessions": revoked, "force_relogin": revoked > 0,
	})
}
