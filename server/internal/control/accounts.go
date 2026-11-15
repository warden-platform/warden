package control

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

// validateRoleChange keeps the guard rails testable without a database:
// the seeded break-glass admin is immutable, and demoting the last federated
// platform administrator (or demoting yourself) would lock the console down.
func validateRoleChange(targetID, actorID, currentRole, newRole string, otherPlatformAdmins int) (string, bool) {
	if !validAccountRole(newRole) {
		return "INVALID_ACCOUNT_ROLE", false
	}
	if targetID == adminPrincipalID {
		return "BUILTIN_ADMIN_PROTECTED", false
	}
	if currentRole == RolePlatformAdmin && newRole != RolePlatformAdmin {
		if actorID == targetID {
			return "CANNOT_DEMOTE_SELF", false
		}
		if otherPlatformAdmins == 0 {
			return "LAST_PLATFORM_ADMIN", false
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
	LastLoginAt     *time.Time `json:"last_login_at"`
	CreatedAt       time.Time  `json:"created_at"`
	IsBuiltinAdmin  bool       `json:"is_builtin_admin"`
	IsSelf          bool       `json:"is_self"`
	ProviderIssuer  string     `json:"provider_issuer,omitempty"`
	ProviderSubject string     `json:"provider_subject,omitempty"`
}

func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	actorID := ""
	actorTenant := platformTenantSlug
	if identity := identityFromContext(r.Context()); identity != nil {
		if identity.Principal != nil {
			actorID = identity.Principal.ID
		}
		if identity.TenantID != "" {
			actorTenant = identity.TenantID
		}
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT p.id::text, p.name, p.status, p.tenant_id,
		       COALESCE(NULLIF(p.metadata->>'display_name',''), NULLIF(f.display_name,''), ''),
		       COALESCE(NULLIF(p.metadata->>'email',''), NULLIF(f.email,''), ''),
		       COALESCE(NULLIF(p.metadata->>'avatar_url',''), ''),
		       COALESCE(NULLIF(p.metadata->>'source',''), CASE WHEN f.id IS NOT NULL THEN 'oidc' ELSE 'local' END),
		       CASE WHEN p.id::text=$1 THEN 'platform_admin' ELSE COALESCE(NULLIF(p.metadata->>'role',''),'user') END,
		       p.last_login_at, p.created_at,
		       p.id::text=$1, p.id::text=$2,
		       COALESCE(f.issuer,''), COALESCE(f.subject,'')
		FROM principals p
		LEFT JOIN LATERAL (
			SELECT id, issuer, subject, email, display_name
			FROM federated_identities WHERE principal_id=p.id
			ORDER BY last_login_at DESC NULLS LAST LIMIT 1
		) f ON TRUE
		WHERE p.kind='user' AND p.tenant_id=$3
		ORDER BY (p.id::text=$1) DESC, p.created_at DESC`, adminPrincipalID, actorID, actorTenant)
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
			&a.IsBuiltinAdmin, &a.IsSelf, &a.ProviderIssuer, &a.ProviderSubject); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取账号信息失败")
			return
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
	if h.pool == nil {
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
	if identity := identityFromContext(r.Context()); identity != nil {
		if identity.Principal != nil {
			actorID = identity.Principal.ID
			actorType = string(identity.Principal.Kind)
		}
		if identity.TenantID != "" {
			actorTenant = identity.TenantID
		}
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Every lookup is tenant-scoped: an administrator in tenant A can never
	// read or change an account that belongs to tenant B.
	var currentRole, name, displayName string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(metadata->>'role',''),'user'), name,
		       COALESCE(NULLIF(metadata->>'display_name',''), '')
		FROM principals
		WHERE id::text=$1 AND kind='user' AND tenant_id=$2 FOR UPDATE`, targetID, actorTenant).
		Scan(&currentRole, &name, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询账号失败")
		return
	}

	var otherPlatformAdmins int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM principals
		WHERE kind='user' AND metadata->>'role'='platform_admin' AND tenant_id=$3
		  AND id::text <> $1 AND id::text <> $2`, targetID, adminPrincipalID, actorTenant).Scan(&otherPlatformAdmins); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "校验管理员数量失败")
		return
	}
	if code, ok := validateRoleChange(targetID, actorID, currentRole, newRole, otherPlatformAdmins); !ok {
		messages := map[string]string{
			"INVALID_ACCOUNT_ROLE":    "角色必须是 user、tenant_admin 或 platform_admin",
			"BUILTIN_ADMIN_PROTECTED": "内置超级管理员账号不可修改（故障应急入口）",
			"CANNOT_DEMOTE_SELF":      "不能给自己降权，请由另一个超级管理员操作",
			"LAST_PLATFORM_ADMIN":     "必须保留至少一个单点登录的超级管理员",
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
		WHERE id::text=$1 AND tenant_id=$3`, targetID, newRole, actorTenant); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新角色失败")
		return
	}
	// Revoke live sessions so a demotion takes effect immediately instead of
	// waiting for the old JWT (and its pam:admin scope) to expire.
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
			"revoked_sessions": revoked, "target_name": name,
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
		if err := h.audit.CreateTx(ctx, tx, event); err != nil {
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
