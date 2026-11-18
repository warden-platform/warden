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
	"github.com/jackc/pgx/v5/pgconn"
)

// PrincipalInvitation is a pending offer for a trusted email to join a specific
// organization with a fixed role. It never creates or elevates an identity by
// itself; only an authenticated federated identity whose trusted email matches
// may accept it, and only while that identity is still unaffiliated.
type PrincipalInvitation struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}

func validInvitationEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 320 {
		return false
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at != strings.LastIndexByte(email, '@') || at == len(email)-1 {
		return false
	}
	domainPart := email[at+1:]
	if !strings.Contains(domainPart, ".") || strings.HasPrefix(domainPart, ".") || strings.HasSuffix(domainPart, ".") {
		return false
	}
	return !strings.ContainsAny(email, " \t\r\n\\\"'")
}

// trustedIdentityEmail returns the IdP-asserted email carried in the principal
// metadata. It is only ever populated by the OIDC provisioning path, so it is
// the trusted value against which invitation matching is done — never a
// user-typed email.
func trustedIdentityEmail(identity *AuthenticatedIdentity) string {
	if identity == nil || identity.Principal == nil {
		return ""
	}
	if value, ok := identity.Principal.Metadata["email"].(string); ok {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

// CreateInvitation lets a tenant sub-administrator invite a trusted email into
// their own organization. The destination tenant is taken from the session, so
// a caller can never invite into another tenant.
func (h *Handler) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	identity := identityFromContext(r.Context())
	tenantID := domain.TenantIDFromContext(r.Context())
	if identity == nil || tenantID == "" || isUnaffiliated(tenantID) {
		jsonError(w, http.StatusForbidden, "ORGANIZATION_REQUIRED", "当前身份未归属任何组织，无法发出邀请")
		return
	}
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !validInvitationEmail(email) {
		jsonError(w, http.StatusBadRequest, "INVALID_EMAIL", "邮箱格式无效")
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = RoleUser
	}
	if !validAssignableRole(role) {
		jsonError(w, http.StatusBadRequest, "INVALID_ACCOUNT_ROLE", "邀请角色只能是 user 或 tenant_admin")
		return
	}
	actorID := ""
	if identity.Principal != nil {
		actorID = identity.Principal.ID
	}
	var invitation PrincipalInvitation
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO principal_invitations(tenant_id,email,role,status,invited_by)
		VALUES($1,$2,$3,'PENDING',NULLIF($4,'')::uuid)
		RETURNING id::text,tenant_id,email,role,status,created_at`,
		tenantID, email, role, actorID).
		Scan(&invitation.ID, &invitation.TenantID, &invitation.Email, &invitation.Role, &invitation.Status, &invitation.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			jsonError(w, http.StatusConflict, "INVITATION_EXISTS", "该邮箱已有一个待处理邀请")
			return
		}
		log.Printf("create invitation failed: %v", err)
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "创建邀请失败")
		return
	}
	jsonResp(w, http.StatusCreated, invitation)
}

// ListInvitations returns the current organization's invitations (all statuses)
// newest first, so a sub-administrator can see pending/accepted/revoked records.
func (h *Handler) ListInvitations(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	identity := identityFromContext(r.Context())
	tenantID := domain.TenantIDFromContext(r.Context())
	if identity == nil || tenantID == "" || isUnaffiliated(tenantID) {
		jsonError(w, http.StatusForbidden, "ORGANIZATION_REQUIRED", "当前身份未归属任何组织")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT id::text,tenant_id,email,role,status,created_at,accepted_at
		FROM principal_invitations WHERE tenant_id=$1
		ORDER BY created_at DESC LIMIT 200`, tenantID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询邀请失败")
		return
	}
	defer rows.Close()
	out := []PrincipalInvitation{}
	for rows.Next() {
		var it PrincipalInvitation
		if err := rows.Scan(&it.ID, &it.TenantID, &it.Email, &it.Role, &it.Status, &it.CreatedAt, &it.AcceptedAt); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取邀请失败")
			return
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取邀请失败")
		return
	}
	jsonResp(w, http.StatusOK, out)
}

// RevokeInvitation cancels a pending invitation belonging to the caller's
// organization. Already accepted/revoked invitations are left untouched.
func (h *Handler) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	identity := identityFromContext(r.Context())
	tenantID := domain.TenantIDFromContext(r.Context())
	if identity == nil || tenantID == "" || isUnaffiliated(tenantID) {
		jsonError(w, http.StatusForbidden, "ORGANIZATION_REQUIRED", "当前身份未归属任何组织")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "缺少邀请标识")
		return
	}
	tag, err := h.pool.Exec(r.Context(), `
		UPDATE principal_invitations SET status='REVOKED', updated_at=now()
		WHERE id::text=$1 AND tenant_id=$2 AND status='PENDING'`, id, tenantID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销邀请失败")
		return
	}
	if tag.RowsAffected() == 0 {
		jsonError(w, http.StatusNotFound, "INVITATION_NOT_FOUND", "待处理邀请不存在")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"id": id, "status": "REVOKED"})
}

// MyInvitations lists pending invitations that match the authenticated
// identity's trusted email, across all organizations, for the unaffiliated
// waiting screen. No business scope is required — only a valid session.
func (h *Handler) MyInvitations(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	email := trustedIdentityEmail(identityFromContext(r.Context()))
	if email == "" {
		jsonResp(w, http.StatusOK, []PrincipalInvitation{})
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT i.id::text, i.tenant_id, i.email, i.role, i.status, i.created_at, NULL::timestamptz, COALESCE(t.name, i.tenant_id)
		FROM principal_invitations i
		JOIN tenants t ON t.slug = i.tenant_id
		WHERE i.status='PENDING' AND lower(i.email)=$1 AND t.status='ACTIVE'
		ORDER BY i.created_at DESC`, email)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询我的邀请失败")
		return
	}
	defer rows.Close()
	type myInvitation struct {
		PrincipalInvitation
		TenantName string `json:"tenant_name"`
	}
	out := []myInvitation{}
	for rows.Next() {
		var it myInvitation
		if err := rows.Scan(&it.ID, &it.TenantID, &it.Email, &it.Role, &it.Status, &it.CreatedAt, &it.AcceptedAt, &it.TenantName); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取我的邀请失败")
			return
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取我的邀请失败")
		return
	}
	jsonResp(w, http.StatusOK, out)
}

// AcceptInvitation lets the authenticated unaffiliated identity join the
// organization of a pending invitation that matches its trusted email. On
// success the identity's own pending session is revoked (via the shared move
// helper) so it re-logs in with real tenant capabilities.
func (h *Handler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "未认证")
		return
	}
	if !isUnaffiliated(identity.TenantID) {
		jsonError(w, http.StatusConflict, "ALREADY_AFFILIATED", "该身份已加入组织，无法接受邀请")
		return
	}
	email := trustedIdentityEmail(identity)
	if email == "" {
		jsonError(w, http.StatusBadRequest, "NO_VERIFIED_EMAIL", "你的登录身份没有可信邮箱，请联系管理员直接指派")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "缺少邀请标识")
		return
	}
	myID := identity.Principal.ID
	ctx := r.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开启事务失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inviteTenant, inviteEmail, inviteRole, tenantStatus string
	err = tx.QueryRow(ctx, `
		SELECT i.tenant_id, lower(i.email), i.role, t.status
		FROM principal_invitations i
		JOIN tenants t ON t.slug = i.tenant_id
		WHERE i.id::text=$1 AND i.status='PENDING'
		FOR UPDATE OF i`, id).Scan(&inviteTenant, &inviteEmail, &inviteRole, &tenantStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusNotFound, "INVITATION_NOT_FOUND", "待处理邀请不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取邀请失败")
		return
	}
	if inviteEmail != email {
		jsonError(w, http.StatusForbidden, "EMAIL_MISMATCH", "邀请邮箱与当前登录身份不一致")
		return
	}
	if tenantStatus != "ACTIVE" {
		jsonError(w, http.StatusConflict, "TENANT_DISABLED", "目标组织已停用")
		return
	}

	name, displayName, revoked, err := movePrincipalBetweenTenantsTx(ctx, tx, myID, unaffiliatedTenant, inviteTenant, inviteRole)
	if errors.Is(err, errUnaffiliatedPrincipalGone) {
		jsonError(w, http.StatusConflict, "ALREADY_AFFILIATED", "该身份已被分配组织")
		return
	}
	if errors.Is(err, errNameTakenInTenant) {
		jsonError(w, http.StatusConflict, "NAME_TAKEN_IN_TENANT", "该组织已存在同名身份，请联系管理员")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "接受邀请失败")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE principal_invitations SET status='ACCEPTED', accepted_at=now(), updated_at=now()
		WHERE id::text=$1`, id); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新邀请状态失败")
		return
	}

	label := displayName
	if label == "" {
		label = name
	}
	if h.audit != nil {
		metadata := map[string]any{
			"previous_tenant": unaffiliatedTenant, "new_tenant": inviteTenant,
			"new_role": inviteRole, "revoked_sessions": revoked, "via": "invitation", "invitation_id": id,
		}
		event := &domain.AuditEvent{
			Action: "account.assigned", ActorID: myID, ActorType: "user",
			TargetID: myID, Summary: "通过邀请加入组织: " + label, Metadata: metadata,
		}
		if err := h.audit.CreateTx(domain.WithTenantID(ctx, inviteTenant), tx, event); err != nil {
			log.Printf("invitation accept audit failed: %v", err)
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "写入审计失败")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "提交失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"id": myID, "tenant": inviteTenant, "role": inviteRole,
		"revoked_sessions": revoked, "force_relogin": true,
	})
}
