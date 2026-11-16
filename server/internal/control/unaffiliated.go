package control

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

// Sentinel errors from the shared relocation helper so each caller can map them
// to the correct HTTP status.
var (
	errUnaffiliatedPrincipalGone = errors.New("unaffiliated principal not found")
	errNameTakenInTenant         = errors.New("name already exists in target tenant")
)

// movePrincipalBetweenTenantsTx relocates a user principal from one tenant to
// another inside the caller's transaction. It is the single place that knows how
// to move an identity across tenants, so the assign (unaffiliated→org), accept
// (unaffiliated→org) and remove (org→unaffiliated) paths can never drift.
//
// It handles the federated_identities composite foreign key
// (principal_id,tenant_id)→principals(id,tenant_id), which Postgres validates
// per statement — so the federation rows are captured, deleted, the principal
// is moved, then the rows are re-inserted under the destination tenant. It also
// revokes the principal's sessions so the membership change takes effect on the
// next login. The caller must have validated that fromTenant is where the
// principal currently lives and that toTenant is an active tenant, and is
// responsible for auditing and commit/rollback.
func movePrincipalBetweenTenantsTx(ctx context.Context, tx pgx.Tx, principalID, fromTenant, toTenant, newRole string) (name, displayName string, revoked int, err error) {
	err = tx.QueryRow(ctx, `
		SELECT name, COALESCE(NULLIF(metadata->>'display_name',''),'')
		FROM principals WHERE id::text=$1 AND kind='user' AND tenant_id=$2 FOR UPDATE`,
		principalID, fromTenant).Scan(&name, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, errUnaffiliatedPrincipalGone
	}
	if err != nil {
		return "", "", 0, err
	}
	var dupe int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM principals WHERE kind='user' AND name=$1 AND tenant_id=$2`, name, toTenant).Scan(&dupe); err != nil {
		return "", "", 0, err
	}
	if dupe > 0 {
		return "", "", 0, errNameTakenInTenant
	}
	type fedRow struct {
		issuer, subject, email, displayName, status string
		lastLoginAt                                 *time.Time
	}
	var fedRows []fedRow
	frows, err := tx.Query(ctx, `
		SELECT issuer, subject, COALESCE(email,''), COALESCE(display_name,''), status, last_login_at
		FROM federated_identities WHERE principal_id=$1::uuid`, principalID)
	if err != nil {
		return "", "", 0, err
	}
	for frows.Next() {
		var row fedRow
		if err := frows.Scan(&row.issuer, &row.subject, &row.email, &row.displayName, &row.status, &row.lastLoginAt); err != nil {
			frows.Close()
			return "", "", 0, err
		}
		fedRows = append(fedRows, row)
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return "", "", 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM federated_identities WHERE principal_id=$1::uuid`, principalID); err != nil {
		return "", "", 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE principals SET tenant_id=$2,
			metadata = jsonb_strip_nulls(metadata || jsonb_build_object('role',$3::text)),
			updated_at=now()
		WHERE id::text=$1`, principalID, toTenant, newRole); err != nil {
		return "", "", 0, err
	}
	for _, row := range fedRows {
		if _, err := tx.Exec(ctx, `
			INSERT INTO federated_identities(tenant_id,issuer,subject,principal_id,email,display_name,status,last_login_at)
			VALUES($1,$2,$3,$4::uuid,NULLIF($5,''),NULLIF($6,''),$7,$8)`,
			toTenant, row.issuer, row.subject, principalID, row.email, row.displayName, row.status, row.lastLoginAt); err != nil {
			return "", "", 0, err
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE admin_sessions SET revoked_at=now() WHERE principal_id::text=$1 AND revoked_at IS NULL`, principalID)
	if err != nil {
		return "", "", 0, err
	}
	return name, displayName, int(tag.RowsAffected()), nil
}

// UnaffiliatedIdentity is a federated SSO principal that authenticated but has
// not been placed into any business organization yet. Its principals.tenant_id
// holds the unaffiliated sentinel. The super administrator may assign it to a
// real tenant here; a tenant invitation (stage 2) can do the same from the
// organization side. Either way the move re-binds federated_identities so the
// next SSO login lands in the chosen organization instead of erroring with a
// tenant mismatch against the old sentinel binding.
type UnaffiliatedIdentity struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email"`
	AvatarURL   string     `json:"avatar_url"`
	Issuer      string     `json:"issuer"`
	Subject     string     `json:"subject"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

// ListUnaffiliatedIdentities returns every authenticated-but-unassigned SSO
// identity so the platform super administrator can see who is waiting to be
// pulled into an organization. Gated to pam:admin + platform tenant.
func (h *Handler) ListUnaffiliatedIdentities(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT p.id::text, p.name,
			COALESCE(NULLIF(p.metadata->>'display_name',''), NULLIF(f.display_name,''), ''),
			COALESCE(NULLIF(p.metadata->>'email',''), NULLIF(f.email,''), ''),
			COALESCE(NULLIF(p.metadata->>'avatar_url',''), ''),
			COALESCE(f.issuer,''), COALESCE(f.subject,''), p.created_at, p.last_login_at
		FROM principals p
		LEFT JOIN LATERAL (
			SELECT issuer, subject, email, display_name
			FROM federated_identities WHERE principal_id = p.id
			ORDER BY last_login_at DESC NULLS LAST LIMIT 1
		) f ON TRUE
		WHERE p.kind='user' AND p.tenant_id=$1 AND p.status='ACTIVE'
		ORDER BY p.created_at DESC`, unaffiliatedTenant)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询未归属身份失败")
		return
	}
	defer rows.Close()
	out := []UnaffiliatedIdentity{}
	for rows.Next() {
		var it UnaffiliatedIdentity
		if err := rows.Scan(&it.ID, &it.Name, &it.DisplayName, &it.Email, &it.AvatarURL,
			&it.Issuer, &it.Subject, &it.CreatedAt, &it.LastLoginAt); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取未归属身份失败")
			return
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取未归属身份失败")
		return
	}
	jsonResp(w, http.StatusOK, out)
}

// assignUnaffiliatedRequest carries the destination organization and the role
// granted on arrival. Role defaults to user (least privilege).
type assignUnaffiliatedRequest struct {
	Tenant string `json:"tenant"`
	Role   string `json:"role"`
}

// AssignUnaffiliatedIdentity moves an unaffiliated SSO principal into a real
// tenant as either a member or a tenant administrator. It is the platform-side
// answer to "who pulls a stranger into an organization": only the single super
// administrator may do it here. The operation re-binds the federated identity
// and revokes the current pending-state session so the effect is immediate and
// consistent on the next login.
func (h *Handler) AssignUnaffiliatedIdentity(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	targetID := strings.TrimSpace(r.PathValue("id"))
	if targetID == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "缺少身份标识")
		return
	}
	var req assignUnaffiliatedRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	targetTenant := strings.ToLower(strings.TrimSpace(req.Tenant))
	if targetTenant == "" || isUnaffiliated(targetTenant) || strings.ContainsAny(targetTenant, "/ \\\"' ") {
		jsonError(w, http.StatusBadRequest, "INVALID_TARGET_TENANT", "目标组织标识无效")
		return
	}
	if targetTenant == platformTenantSlug {
		jsonError(w, http.StatusBadRequest, "PLATFORM_DOMAIN_PROTECTED", "平台管理域不是业务组织，不能作为指派目标")
		return
	}
	newRole := strings.TrimSpace(req.Role)
	if newRole == "" {
		newRole = RoleUser
	}
	if !validAssignableRole(newRole) {
		jsonError(w, http.StatusBadRequest, "INVALID_ACCOUNT_ROLE", "指派角色只能是 user 或 tenant_admin")
		return
	}

	ctx := r.Context()
	var active bool
	if err := h.pool.QueryRow(ctx, `SELECT status='ACTIVE' FROM tenants WHERE slug=$1`, targetTenant).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "目标组织不存在")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询目标组织失败")
		return
	}
	if !active {
		jsonError(w, http.StatusConflict, "TENANT_DISABLED", "目标组织已停用")
		return
	}

	actorID, actorType := "", "user"
	if identity := identityFromContext(ctx); identity != nil {
		if identity.Principal != nil {
			actorID = identity.Principal.ID
			actorType = string(identity.Principal.Kind)
		}
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	name, displayName, revoked, err := movePrincipalBetweenTenantsTx(ctx, tx, targetID, unaffiliatedTenant, targetTenant, newRole)
	if errors.Is(err, errUnaffiliatedPrincipalGone) {
		jsonError(w, http.StatusNotFound, "IDENTITY_NOT_FOUND", "未归属身份不存在或已被分配")
		return
	}
	if errors.Is(err, errNameTakenInTenant) {
		jsonError(w, http.StatusConflict, "NAME_TAKEN_IN_TENANT", "该组织已存在同名身份，无法指派")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "指派身份失败")
		return
	}

	label := displayName
	if label == "" {
		label = name
	}
	if h.audit != nil {
		metadata := map[string]any{
			"previous_tenant": unaffiliatedTenant, "new_tenant": targetTenant,
			"new_role": newRole, "revoked_sessions": revoked, "target_name": name,
		}
		event := &domain.AuditEvent{
			Action: "account.assigned", ActorID: actorID, ActorType: actorType,
			TargetID: targetID, Summary: "未归属身份已分配至组织: " + label, Metadata: metadata,
		}
		if identity := identityFromContext(ctx); identity != nil {
			event.CredentialID = identity.CredentialID
			for key, value := range identity.AuditMetadata {
				metadata[key] = value
			}
		}
		auditCtx := domain.WithTenantID(ctx, targetTenant)
		if err := h.audit.CreateTx(auditCtx, tx, event); err != nil {
			log.Printf("unaffiliated assignment audit failed: %v", err)
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "写入分配审计失败")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "提交分配失败")
		return
	}

	jsonResp(w, http.StatusOK, map[string]any{
		"id": targetID, "tenant": targetTenant, "role": newRole,
		"revoked_sessions": revoked, "force_relogin": revoked > 0,
	})
}
