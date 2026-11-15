package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func (h *Handler) CreateTenantAdmin(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 255 || len(req.Password) < 8 || len(req.Password) > 72 {
		jsonError(w, http.StatusBadRequest, "INVALID_ADMIN_USER", "管理员名称或密码格式无效")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	var tenantID string
	if err := h.pool.QueryRow(r.Context(), `SELECT slug FROM tenants WHERE slug=$1 AND status='ACTIVE'`, slug).Scan(&tenantID); err != nil {
		jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在或已停用")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PASSWORD_HASH_FAILED", "管理员密码处理失败")
		return
	}
	var p domain.Principal
	err = h.pool.QueryRow(r.Context(), `INSERT INTO principals
		(kind, name, status, tenant_id, metadata, password_hash)
		VALUES ('user',$1,'ACTIVE',$2,'{"role":"tenant_admin"}'::jsonb,$3)
		RETURNING id, kind, name, status, tenant_id, metadata, created_at, updated_at`, req.Name, tenantID, hash).
		Scan(&p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID, &p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		jsonError(w, http.StatusConflict, "TENANT_ADMIN_CREATE_FAILED", "管理员名称已存在或数据无效")
		return
	}
	jsonResp(w, http.StatusCreated, p)
}

func (h *Handler) ListTenantAdmins(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	if !h.tenantIsActive(r.Context(), slug) {
		var exists bool
		if err := h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM tenants WHERE slug=$1)`, slug).Scan(&exists); err != nil || !exists {
			jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在")
			return
		}
	}
	rows, err := h.pool.Query(r.Context(), `SELECT id,kind,name,status,tenant_id,metadata,last_login_at,created_at,updated_at
		FROM principals WHERE tenant_id=$1 AND kind='user' AND metadata->>'role'='tenant_admin' ORDER BY created_at`, slug)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询租户管理员失败")
		return
	}
	defer rows.Close()
	admins := []domain.Principal{}
	for rows.Next() {
		var admin domain.Principal
		if err := rows.Scan(&admin.ID, &admin.Kind, &admin.Name, &admin.Status, &admin.TenantID, &admin.Metadata, &admin.LastLoginAt, &admin.CreatedAt, &admin.UpdatedAt); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取租户管理员失败")
			return
		}
		admins = append(admins, admin)
	}
	jsonResp(w, http.StatusOK, admins)
}
