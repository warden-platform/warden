package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"gitee.com/pam-platform/warden/server/internal/storage/postgres"
	"github.com/jackc/pgx/v5"
)

func (h *Handler) ListProjectUsers(w http.ResponseWriter, r *http.Request) {
	if h.projectUsers == nil {
		databaseUnavailable(w)
		return
	}
	users, err := h.projectUsers.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询项目用户失败")
		return
	}
	jsonResp(w, http.StatusOK, users)
}

func (h *Handler) GetProjectUser(w http.ResponseWriter, r *http.Request) {
	if h.projectUsers == nil {
		databaseUnavailable(w)
		return
	}
	user, err := h.projectUsers.GetByID(r.Context(), r.PathValue("principalId"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询项目用户失败")
		return
	}
	if user == nil {
		projectUserNotFound(w)
		return
	}
	jsonResp(w, http.StatusOK, user)
}

func (h *Handler) UpdateProjectUserStatus(w http.ResponseWriter, r *http.Request) {
	if h.projectUsers == nil || h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var input struct {
		Status domain.PrincipalStatus `json:"status"`
	}
	if !decodeProjectUserJSON(w, r, &input) {
		return
	}
	if input.Status != domain.PrincipalStatusActive && input.Status != domain.PrincipalStatusDisabled {
		jsonError(w, http.StatusBadRequest, "INVALID_PRINCIPAL_STATUS", "用户状态必须是 ACTIVE 或 DISABLED")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新项目用户失败")
		return
	}
	defer tx.Rollback(ctx)
	revoked, err := h.projectUsers.UpdateStatusTx(ctx, tx, r.PathValue("principalId"), input.Status)
	if errors.Is(err, postgres.ErrProjectUserNotFound) {
		projectUserNotFound(w)
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新项目用户失败")
		return
	}
	metadata := map[string]any{"status": input.Status, "revoked_sessions": revoked}
	if err := h.createProjectUserAuditTx(ctx, tx, "principal.status.updated", r.PathValue("principalId"), metadata); err != nil {
		jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "记录用户状态变更失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新项目用户失败")
		return
	}
	h.respondWithProjectUser(w, r, "读取项目用户失败")
}

func (h *Handler) ResetProjectUserPassword(w http.ResponseWriter, r *http.Request) {
	if h.projectUsers == nil || h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeProjectUserJSON(w, r, &input) {
		return
	}
	if len(input.Password) < 8 || len(input.Password) > 72 {
		jsonError(w, http.StatusBadRequest, "INVALID_PASSWORD", "密码长度必须为 8-72 字节")
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PASSWORD_HASH_FAILED", "用户密码处理失败")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "重置项目用户密码失败")
		return
	}
	defer tx.Rollback(ctx)
	revoked, err := h.projectUsers.UpdatePasswordTx(ctx, tx, r.PathValue("principalId"), hash)
	if errors.Is(err, postgres.ErrProjectUserNotFound) {
		projectUserNotFound(w)
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "重置项目用户密码失败")
		return
	}
	if err := h.createProjectUserAuditTx(ctx, tx, "principal.password.reset", r.PathValue("principalId"), map[string]any{"revoked_sessions": revoked}); err != nil {
		jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "记录密码重置失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "重置项目用户密码失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"status": "password_reset", "revoked_sessions": revoked})
}

func (h *Handler) RevokeProjectUserSessions(w http.ResponseWriter, r *http.Request) {
	if h.projectUsers == nil || h.pool == nil {
		databaseUnavailable(w)
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销用户会话失败")
		return
	}
	defer tx.Rollback(ctx)
	revoked, err := h.projectUsers.RevokeSessionsTx(ctx, tx, r.PathValue("principalId"))
	if errors.Is(err, postgres.ErrProjectUserNotFound) {
		projectUserNotFound(w)
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销用户会话失败")
		return
	}
	if err := h.createProjectUserAuditTx(ctx, tx, "user.sessions.revoked", r.PathValue("principalId"), map[string]any{"revoked_sessions": revoked}); err != nil {
		jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "记录会话撤销失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "撤销用户会话失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"status": "sessions_revoked", "revoked_sessions": revoked})
}

func (h *Handler) ChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	if h.projectUsers == nil || h.pool == nil || h.principals == nil {
		databaseUnavailable(w)
		return
	}
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil || identity.Kind != "JWT" || identity.SessionID == "" {
		jsonError(w, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRES_LOGIN", "请使用网页登录会话修改密码")
		return
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeProjectUserJSON(w, r, &input) {
		return
	}
	if len(input.CurrentPassword) < 8 || len(input.CurrentPassword) > 72 || len(input.NewPassword) < 8 || len(input.NewPassword) > 72 {
		jsonError(w, http.StatusBadRequest, "INVALID_PASSWORD", "当前密码和新密码长度必须为 8-72 字节")
		return
	}
	if input.CurrentPassword == input.NewPassword {
		jsonError(w, http.StatusBadRequest, "PASSWORD_UNCHANGED", "新密码不能与当前密码相同")
		return
	}
	principal, err := h.principals.GetByID(r.Context(), identity.Principal.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "校验当前密码失败")
		return
	}
	if principal == nil || principal.PasswordHash == "" || !verifyPassword(input.CurrentPassword, principal.PasswordHash) {
		jsonError(w, http.StatusForbidden, "CURRENT_PASSWORD_INVALID", "当前密码不正确")
		return
	}
	hash, err := hashPassword(input.NewPassword)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PASSWORD_HASH_FAILED", "用户密码处理失败")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "修改密码失败")
		return
	}
	defer tx.Rollback(ctx)
	revoked, err := h.projectUsers.UpdateOwnPasswordTx(ctx, tx, principal.ID, principal.PasswordHash, hash)
	if errors.Is(err, postgres.ErrProjectUserNotFound) {
		jsonError(w, http.StatusConflict, "PASSWORD_CHANGED_RETRY", "密码已发生变化，请重新登录后再试")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "修改密码失败")
		return
	}
	if err := h.createProjectUserAuditTx(ctx, tx, "user.password.changed", principal.ID, map[string]any{"revoked_sessions": revoked}); err != nil {
		jsonError(w, http.StatusInternalServerError, "AUDIT_WRITE_FAILED", "记录密码变更失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "修改密码失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "pam_session", Value: "", Path: "/", HttpOnly: true,
		Secure: os.Getenv("WARDEN_COOKIE_SECURE") != "false", SameSite: http.SameSiteStrictMode, MaxAge: -1})
	jsonResp(w, http.StatusOK, map[string]any{"status": "password_changed", "revoked_sessions": revoked})
}

func decodeProjectUserJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (h *Handler) createProjectUserAuditTx(ctx context.Context, tx pgx.Tx, action, targetID string, metadata map[string]any) error {
	if h.audit == nil {
		return errors.New("audit repository is unavailable")
	}
	identity := identityFromContext(ctx)
	event := &domain.AuditEvent{Action: action, TargetID: targetID, Summary: action, Metadata: metadata}
	if identity != nil && identity.Principal != nil {
		event.ActorID = identity.Principal.ID
		event.ActorType = string(identity.Principal.Kind)
	}
	return h.audit.CreateTx(ctx, tx, event)
}

func (h *Handler) respondWithProjectUser(w http.ResponseWriter, r *http.Request, failureMessage string) {
	user, err := h.projectUsers.GetByID(r.Context(), r.PathValue("principalId"))
	if err != nil || user == nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", failureMessage)
		return
	}
	jsonResp(w, http.StatusOK, user)
}

func projectUserNotFound(w http.ResponseWriter) {
	jsonError(w, http.StatusNotFound, "PROJECT_USER_NOT_FOUND", "项目用户不存在")
}
