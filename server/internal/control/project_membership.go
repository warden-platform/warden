package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"gitee.com/pam-platform/warden/server/internal/storage/postgres"
)

type projectMembershipAccessError struct{ Code, Message string }

func (e *projectMembershipAccessError) Error() string { return e.Message }

func (h *Handler) projectMembershipEnforced(ctx context.Context) bool {
	if h.pool == nil {
		return false
	}
	enforced, err := h.loadProjectMembershipPolicy(ctx)
	if err != nil {
		// Authorization settings fail closed. Tenant administrators still bypass
		// project membership, while ordinary identities are forced through the
		// membership queries that will surface a database error.
		return true
	}
	return enforced
}

func (h *Handler) loadProjectMembershipPolicy(ctx context.Context) (bool, error) {
	var enforced bool
	if err := h.pool.QueryRow(ctx, `SELECT COALESCE((settings->>'projectMembershipEnforcement')::boolean,false) FROM tenants WHERE slug=$1`, domain.TenantIDFromContext(ctx)).Scan(&enforced); err != nil {
		return false, err
	}
	return enforced, nil
}

func (h *Handler) projectAccess(ctx context.Context, projectID string, execute bool) error {
	if !h.projectMembershipEnforced(ctx) {
		return nil
	}
	identity := identityFromContext(ctx)
	if identity == nil || identity.Principal == nil {
		return &projectMembershipAccessError{"UNAUTHORIZED", "未认证"}
	}
	if identity.IsTenantAdministrator() {
		return nil
	}
	role, status, err := h.projectMemberships.Role(ctx, projectID, identity.Principal.ID)
	if err != nil {
		return err
	}
	if status != domain.ProjectMembershipActive {
		return &projectMembershipAccessError{"PROJECT_ACCESS_DENIED", "当前身份不是该项目的有效成员"}
	}
	if execute && role == domain.ProjectRoleViewer {
		return &projectMembershipAccessError{"PROJECT_ROLE_DENIED", "VIEWER 角色不能发起执行"}
	}
	return nil
}

func (h *Handler) principalProjectAccess(ctx context.Context, projectID, principalID string, execute bool) error {
	if !h.projectMembershipEnforced(ctx) {
		return nil
	}
	var administrator bool
	if err := h.pool.QueryRow(ctx, `SELECT id=$3::uuid OR metadata->>'role'='tenant_admin' FROM principals WHERE tenant_id=$1 AND id=$2`, domain.TenantIDFromContext(ctx), principalID, adminPrincipalID).Scan(&administrator); err != nil {
		return err
	}
	if administrator {
		return nil
	}
	role, status, err := h.projectMemberships.Role(ctx, projectID, principalID)
	if err != nil {
		return err
	}
	if status != domain.ProjectMembershipActive {
		return &projectMembershipAccessError{"PROJECT_ACCESS_DENIED", "请求身份已不再是该项目的有效成员"}
	}
	if execute && role == domain.ProjectRoleViewer {
		return &projectMembershipAccessError{"PROJECT_ROLE_DENIED", "请求身份的 VIEWER 角色不能发起执行"}
	}
	return nil
}

func (h *Handler) canManageProject(ctx context.Context, projectID string) error {
	identity := identityFromContext(ctx)
	if identity == nil || identity.Principal == nil {
		return &projectMembershipAccessError{"UNAUTHORIZED", "未认证"}
	}
	if identity.IsTenantAdministrator() {
		return nil
	}
	if h.projectMemberships == nil {
		return errors.New("项目成员服务不可用")
	}
	role, status, err := h.projectMemberships.Role(ctx, projectID, identity.Principal.ID)
	if err != nil {
		return err
	}
	if role != domain.ProjectRoleAdmin || status != domain.ProjectMembershipActive {
		return &projectMembershipAccessError{"PROJECT_ADMIN_REQUIRED", "需要项目管理员权限"}
	}
	return nil
}

func (h *Handler) canViewProjectMembers(ctx context.Context, projectID string) error {
	identity := identityFromContext(ctx)
	if identity == nil || identity.Principal == nil {
		return &projectMembershipAccessError{"UNAUTHORIZED", "未认证"}
	}
	if identity.IsTenantAdministrator() {
		return nil
	}
	_, status, err := h.projectMemberships.Role(ctx, projectID, identity.Principal.ID)
	if err != nil {
		return err
	}
	if status != domain.ProjectMembershipActive {
		return &projectMembershipAccessError{"PROJECT_ACCESS_DENIED", "当前身份不是该项目的有效成员"}
	}
	return nil
}

func (h *Handler) resolveProjectMembershipError(w http.ResponseWriter, err error) bool {
	var accessErr *projectMembershipAccessError
	if errors.As(err, &accessErr) {
		jsonError(w, http.StatusForbidden, accessErr.Code, accessErr.Message)
		return true
	}
	return false
}

func (h *Handler) writeProjectMembershipError(w http.ResponseWriter, err error) {
	if !h.resolveProjectMembershipError(w, err) {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "检查项目权限失败")
	}
}

func (h *Handler) ensureResourceProjectAccess(ctx context.Context, resource *domain.Resource, execute bool) error {
	if resource == nil {
		return &projectMembershipAccessError{"RESOURCE_NOT_FOUND", "资源不存在"}
	}
	if h.projectMemberships == nil || !h.projectMembershipEnforced(ctx) {
		return nil
	}
	if identity := identityFromContext(ctx); identity != nil && identity.IsTenantAdministrator() {
		return nil
	}
	identity := identityFromContext(ctx)
	if identity == nil || identity.Principal == nil {
		return &projectMembershipAccessError{"UNAUTHORIZED", "未认证"}
	}
	scope, err := h.projects.GetResourceScope(ctx, resource.ID)
	if err != nil || scope == nil {
		return err
	}
	ids, err := h.projectMemberships.ProjectIDs(ctx, identity.Principal.ID)
	if err != nil {
		return err
	}
	for _, allowed := range scope.ProjectIDs {
		for _, member := range ids {
			if allowed == member {
				if execute {
					return h.projectAccess(ctx, allowed, true)
				}
				return nil
			}
		}
	}
	return &projectMembershipAccessError{"PROJECT_ACCESS_DENIED", "资源对当前身份不可见"}
}

func (h *Handler) filterResourcesByProjectMembership(ctx context.Context, resources []domain.Resource) ([]domain.Resource, error) {
	if h.projectMemberships == nil || !h.projectMembershipEnforced(ctx) {
		return resources, nil
	}
	identity := identityFromContext(ctx)
	if identity == nil || identity.IsTenantAdministrator() {
		return resources, nil
	}
	ids, err := h.projectMemberships.ProjectIDs(ctx, identity.Principal.ID)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	out := make([]domain.Resource, 0, len(resources))
	for _, resource := range resources {
		if resource.ProjectScopeMode == domain.ProjectScopeUnassigned {
			continue
		}
		visibleIDs := make([]string, 0, len(resource.ProjectIDs))
		visibleNames := make([]string, 0, len(resource.ProjectNames))
		for i, projectID := range resource.ProjectIDs {
			if !allowed[projectID] {
				continue
			}
			visibleIDs = append(visibleIDs, projectID)
			if i < len(resource.ProjectNames) {
				visibleNames = append(visibleNames, resource.ProjectNames[i])
			} else {
				visibleNames = append(visibleNames, "")
			}
		}
		if len(visibleIDs) > 0 {
			resource.ProjectIDs = visibleIDs
			resource.ProjectNames = visibleNames
			if !allowed[resource.ProjectID] {
				resource.ProjectID = visibleIDs[0]
				resource.ProjectName = visibleNames[0]
				resource.ProjectSource = ""
			}
			out = append(out, resource)
		}
	}
	return out, nil
}

func (h *Handler) ListProjectMembers(w http.ResponseWriter, r *http.Request) {
	if h.projectMemberships == nil {
		databaseUnavailable(w)
		return
	}
	if err := h.canViewProjectMembers(r.Context(), r.PathValue("projectId")); err != nil {
		h.writeProjectMembershipError(w, err)
		return
	}
	items, err := h.projectMemberships.List(r.Context(), r.PathValue("projectId"))
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "查询项目成员失败")
		return
	}
	if items == nil {
		items = []domain.ProjectMembership{}
	}
	jsonResp(w, 200, items)
}

func (h *Handler) ListProjectMemberCandidates(w http.ResponseWriter, r *http.Request) {
	if h.projectMemberships == nil {
		databaseUnavailable(w)
		return
	}
	if err := h.canManageProject(r.Context(), r.PathValue("projectId")); err != nil {
		h.writeProjectMembershipError(w, err)
		return
	}
	items, err := h.projectMemberships.Candidates(r.Context(), r.PathValue("projectId"))
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "查询成员候选人失败")
		return
	}
	if items == nil {
		items = []domain.Principal{}
	}
	jsonResp(w, 200, items)
}

func (h *Handler) CreateProjectUser(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil || h.projectMemberships == nil {
		databaseUnavailable(w)
		return
	}
	var input struct {
		Name     string             `json:"name"`
		Password string             `json:"password"`
		Role     domain.ProjectRole `json:"role"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_USER", "用户请求体无效")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 255 || len(input.Password) < 8 || len(input.Password) > 72 ||
		(input.Role != domain.ProjectRoleAdmin && input.Role != domain.ProjectRoleOperator && input.Role != domain.ProjectRoleViewer) {
		jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_USER", "用户名、密码或项目角色格式无效")
		return
	}
	ctx := r.Context()
	projectID := r.PathValue("projectId")
	if project, err := h.projects.GetByID(ctx, projectID); err != nil || project == nil {
		jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在")
		return
	}
	var count, max int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM principals WHERE tenant_id=$1 AND status='ACTIVE'`, domain.TenantIDFromContext(ctx)).Scan(&count); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "检查租户 Principal 配额失败")
		return
	}
	if err := h.pool.QueryRow(ctx, `SELECT max_principals FROM tenant_quotas WHERE tenant_id=$1`, domain.TenantIDFromContext(ctx)).Scan(&max); err == nil && count >= max {
		jsonError(w, http.StatusTooManyRequests, "TENANT_QUOTA_EXCEEDED", "已达到该租户的 Principal 配额")
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PASSWORD_HASH_FAILED", "用户密码处理失败")
		return
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "创建项目用户失败")
		return
	}
	defer tx.Rollback(ctx)
	actor := identityFromContext(ctx)
	var user domain.Principal
	err = tx.QueryRow(ctx, `INSERT INTO principals(kind,name,status,tenant_id,metadata,password_hash)
		VALUES ('user',$1,'ACTIVE',$2,'{"role":"project_user"}'::jsonb,$3)
		RETURNING id,kind,name,status,tenant_id,metadata,created_at,updated_at`, input.Name, domain.TenantIDFromContext(ctx), hash).
		Scan(&user.ID, &user.Kind, &user.Name, &user.Status, &user.TenantID, &user.Metadata, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		jsonError(w, http.StatusConflict, "PROJECT_USER_CREATE_FAILED", "用户名已存在或数据无效")
		return
	}
	command, err := tx.Exec(ctx, `INSERT INTO project_memberships(tenant_id,project_id,principal_id,role,status,created_by)
		VALUES($1,$2,$3,$4,'ACTIVE',$5)`, user.TenantID, projectID, user.ID, input.Role, actor.Principal.ID)
	if err != nil || command.RowsAffected() != 1 {
		jsonError(w, http.StatusInternalServerError, "PROJECT_MEMBERSHIP_CREATE_FAILED", "绑定项目成员失败")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "创建项目用户失败")
		return
	}
	h.writeNotificationAudit(ctx, "principal.created", user.ID, "项目用户已创建: "+user.Name, map[string]any{"project_id": projectID, "role": input.Role}, actor.Principal.ID, string(actor.Principal.Kind))
	h.writeNotificationAudit(ctx, "project.member.added", projectID, "项目成员已添加: "+user.Name, map[string]any{"principal_id": user.ID, "role": input.Role}, actor.Principal.ID, string(actor.Principal.Kind))
	membership, err := h.projectMemberships.List(ctx, projectID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取项目成员失败")
		return
	}
	for _, item := range membership {
		if item.PrincipalID == user.ID {
			jsonResp(w, http.StatusCreated, map[string]any{"user": user, "membership": item})
			return
		}
	}
	jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取新建项目成员失败")
}

type projectMemberInput struct {
	PrincipalID string                         `json:"principalId"`
	Role        domain.ProjectRole             `json:"role"`
	Status      domain.ProjectMembershipStatus `json:"status"`
}

func validateProjectMemberInput(input projectMemberInput, requirePrincipal bool) bool {
	if requirePrincipal && strings.TrimSpace(input.PrincipalID) == "" {
		return false
	}
	if input.Role != domain.ProjectRoleAdmin && input.Role != domain.ProjectRoleOperator && input.Role != domain.ProjectRoleViewer {
		return false
	}
	return input.Status == "" || input.Status == domain.ProjectMembershipActive || input.Status == domain.ProjectMembershipDisabled
}

func (h *Handler) saveProjectMember(w http.ResponseWriter, r *http.Request, projectID, principalID string, create bool) {
	if h.projectMemberships == nil {
		databaseUnavailable(w)
		return
	}
	if err := h.canManageProject(r.Context(), projectID); err != nil {
		h.writeProjectMembershipError(w, err)
		return
	}
	var input projectMemberInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		jsonError(w, 400, "INVALID_PROJECT_MEMBER", "成员请求体无效")
		return
	}
	if create {
		principalID = input.PrincipalID
	} else if principalID == "" {
		jsonError(w, 400, "INVALID_PROJECT_MEMBER", "成员 ID 无效")
		return
	}
	if !validateProjectMemberInput(input, create) {
		jsonError(w, 400, "INVALID_PROJECT_MEMBER", "角色或状态无效")
		return
	}
	status := input.Status
	if status == "" {
		status = domain.ProjectMembershipActive
	}
	actor := identityFromContext(r.Context())
	createdBy := ""
	if actor != nil && actor.Principal != nil {
		createdBy = actor.Principal.ID
	}
	item, err := h.projectMemberships.Upsert(r.Context(), projectID, principalID, input.Role, status, createdBy)
	if err != nil {
		if errors.Is(err, postgres.ErrInvalidProjectMembership) {
			jsonError(w, 400, "INVALID_PROJECT_MEMBER", "项目或身份不存在，或不属于当前租户")
			return
		}
		jsonError(w, 500, "DB_ERROR", "保存项目成员失败")
		return
	}
	action := "project.member.updated"
	if create {
		action = "project.member.added"
	}
	h.writeNotificationAudit(r.Context(), action, projectID, "项目成员已保存: "+item.PrincipalName, map[string]any{"project_id": projectID, "principal_id": principalID, "role": input.Role, "status": status}, createdBy, string(actor.Principal.Kind))
	jsonResp(w, func() int {
		if create {
			return http.StatusCreated
		}
		return http.StatusOK
	}(), item)
}
func (h *Handler) AddProjectMember(w http.ResponseWriter, r *http.Request) {
	h.saveProjectMember(w, r, r.PathValue("projectId"), "", true)
}
func (h *Handler) UpdateProjectMember(w http.ResponseWriter, r *http.Request) {
	h.saveProjectMember(w, r, r.PathValue("projectId"), r.PathValue("principalId"), false)
}
func (h *Handler) RemoveProjectMember(w http.ResponseWriter, r *http.Request) {
	if err := h.canManageProject(r.Context(), r.PathValue("projectId")); err != nil {
		h.writeProjectMembershipError(w, err)
		return
	}
	ok, err := h.projectMemberships.Disable(r.Context(), r.PathValue("projectId"), r.PathValue("principalId"))
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "停用项目成员失败")
		return
	}
	if !ok {
		jsonError(w, 404, "MEMBER_NOT_FOUND", "项目成员不存在")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.member.removed", r.PathValue("projectId"), "项目成员已停用", map[string]any{"project_id": r.PathValue("projectId"), "principal_id": r.PathValue("principalId")}, actor.Principal.ID, string(actor.Principal.Kind))
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) ListMyProjects(w http.ResponseWriter, r *http.Request) {
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil {
		jsonError(w, 401, "UNAUTHORIZED", "未认证")
		return
	}
	var items []domain.Project
	var err error
	if identity.IsTenantAdministrator() || !h.projectMembershipEnforced(r.Context()) {
		items, err = h.projects.List(r.Context())
	} else {
		items, err = h.projectMemberships.ListForPrincipal(r.Context(), identity.Principal.ID)
	}
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "查询可访问项目失败")
		return
	}
	if items == nil {
		items = []domain.Project{}
	}
	jsonResp(w, 200, items)
}
func (h *Handler) GetProjectMembershipPolicy(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	enforced, err := h.loadProjectMembershipPolicy(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询项目成员策略失败")
		return
	}
	jsonResp(w, 200, map[string]bool{"enforced": enforced})
}
func (h *Handler) UpdateProjectMembershipPolicy(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var input struct {
		Enforced bool `json:"enforced"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		jsonError(w, 400, "INVALID_POLICY", "策略请求体无效")
		return
	}
	command, err := h.pool.Exec(r.Context(), `UPDATE tenants SET settings=jsonb_set(COALESCE(settings,'{}'::jsonb),'{projectMembershipEnforcement}',to_jsonb($2::boolean),true),updated_at=now() WHERE slug=$1`, domain.TenantIDFromContext(r.Context()), input.Enforced)
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "保存项目成员策略失败")
		return
	}
	if command.RowsAffected() != 1 {
		jsonError(w, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.membership.enforcement.updated", domain.TenantIDFromContext(r.Context()), "项目成员强制策略已更新", map[string]any{"enforced": input.Enforced}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, 200, map[string]bool{"enforced": input.Enforced})
}
