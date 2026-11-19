package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

// TenantMember is a login identity belonging to a tenant, shown in the tenant
// detail console. Memberships lists the projects the member participates in.
type TenantMember struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	DisplayName    string          `json:"display_name"`
	Email          string          `json:"email"`
	Role           string          `json:"role"`
	Status         string          `json:"status"`
	AuthSource     string          `json:"auth_source"`
	IsBuiltinAdmin bool            `json:"is_builtin_admin"`
	LastLoginAt    *time.Time      `json:"last_login_at"`
	CreatedAt      time.Time       `json:"created_at"`
	Memberships    []MemberProject `json:"memberships"`
}

// MemberProject is a project participation entry for a tenant member.
type MemberProject struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

// ListTenantMembers returns every login user in a tenant (members and
// sub-admins alike), so the super administrator can see who belongs to each
// enterprise. Scoped to the requested tenant; platform-admin only.
func (h *Handler) ListTenantMembers(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	var exists bool
	if err := h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM tenants WHERE slug=$1)`, slug).Scan(&exists); err != nil || !exists {
		jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT p.id::text, p.name, p.status,
		       COALESCE(NULLIF(p.metadata->>'display_name',''), NULLIF(f.display_name,''), ''),
		       COALESCE(NULLIF(p.metadata->>'email',''), NULLIF(f.email,''), ''),
		       CASE WHEN p.id::text=$2 THEN 'platform_admin' ELSE COALESCE(NULLIF(p.metadata->>'role',''),'user') END,
		       COALESCE(NULLIF(p.metadata->>'source',''), CASE WHEN f.id IS NOT NULL THEN 'oidc' ELSE 'local' END),
		       p.id::text=$2, p.last_login_at, p.created_at
		FROM principals p
		LEFT JOIN LATERAL (
			SELECT id, email, display_name FROM federated_identities
			WHERE principal_id=p.id ORDER BY last_login_at DESC NULLS LAST LIMIT 1
		) f ON TRUE
		WHERE p.tenant_id=$1 AND p.kind='user'
		ORDER BY (p.id::text=$2) DESC, p.created_at`, slug, adminPrincipalID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询租户成员失败")
		return
	}
	defer rows.Close()
	members := []TenantMember{}
	for rows.Next() {
		var m TenantMember
		if err := rows.Scan(&m.ID, &m.Name, &m.Status, &m.DisplayName, &m.Email, &m.Role,
			&m.AuthSource, &m.IsBuiltinAdmin, &m.LastLoginAt, &m.CreatedAt); err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取租户成员失败")
			return
		}
		m.Memberships = []MemberProject{}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取租户成员失败")
		return
	}
	for i := range members {
		mrows, err := h.pool.Query(r.Context(), `
			SELECT pm.project_id::text, pr.name, pm.role, pm.status
			FROM project_memberships pm JOIN projects pr ON pr.id=pm.project_id
			WHERE pm.principal_id=$1::uuid AND pm.tenant_id=$2
			ORDER BY pr.name`, members[i].ID, slug)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询成员项目失败")
			return
		}
		for mrows.Next() {
			var mp MemberProject
			if err := mrows.Scan(&mp.ProjectID, &mp.ProjectName, &mp.Role, &mp.Status); err != nil {
				mrows.Close()
				jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取成员项目失败")
				return
			}
			members[i].Memberships = append(members[i].Memberships, mp)
		}
		mrows.Close()
	}
	jsonResp(w, http.StatusOK, members)
}

// CreateTenantMember creates a login user inside a tenant with an assignable
// role (member or sub-admin). The super administrator uses this to populate a
// tenant; the created member logs in with the RAM-style name <name>@<slug>.
func (h *Handler) CreateTenantMember(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = RoleUser
	}
	if !validLoginName(req.Name) || len(req.Password) < 8 || len(req.Password) > 72 || !validAssignableRole(role) {
		jsonError(w, http.StatusBadRequest, "INVALID_MEMBER", "名称、密码或角色格式无效（名称不能含 @，角色为 user/tenant_admin）")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	if !h.tenantIsActive(r.Context(), slug) {
		jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在或已停用")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PASSWORD_HASH_FAILED", "密码处理失败")
		return
	}
	var p domain.Principal
	err = h.pool.QueryRow(r.Context(), `INSERT INTO principals
		(kind, name, status, tenant_id, metadata, password_hash)
		VALUES ('user',$1,'ACTIVE',$2,jsonb_build_object('role',$3::text),$4)
		RETURNING id, kind, name, status, tenant_id, metadata, created_at, updated_at`,
		req.Name, slug, role, hash).
		Scan(&p.ID, &p.Kind, &p.Name, &p.Status, &p.TenantID, &p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		jsonError(w, http.StatusConflict, "MEMBER_CREATE_FAILED", "成员名称已存在或数据无效")
		return
	}
	jsonResp(w, http.StatusCreated, map[string]any{
		"id": p.ID, "name": p.Name, "role": role, "tenant_id": slug,
		"login_name": p.Name + "@" + slug,
	})
}
