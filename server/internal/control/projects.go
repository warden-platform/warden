package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"gitee.com/pam-platform/warden/server/internal/storage/postgres"
	"github.com/jackc/pgx/v5"
)

type resourceConsumerProjectsInput struct {
	ProjectIDs []string `json:"projectIds"`
}

type resourceProjectScopeInput struct {
	Mode       domain.ProjectScopeMode `json:"mode"`
	ProjectIDs []string                `json:"projectIds"`
	Reason     string                  `json:"reason"`
}

func (h *Handler) GetResourceProjectScope(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	scope, err := h.projects.GetResourceScope(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源项目范围失败")
		return
	}
	if scope == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	jsonResp(w, http.StatusOK, scope)
}

func (h *Handler) UpdateResourceProjectScope(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	var input resourceProjectScopeInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.ProjectIDs) > 100 || len([]rune(input.Reason)) > 500 {
		jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_SCOPE", "资源项目范围请求无效")
		return
	}
	actor := identityFromContext(r.Context())
	actorID := ""
	if actor != nil && actor.Principal != nil {
		actorID = actor.Principal.ID
	}
	if err := h.projects.ReplaceResourceScope(r.Context(), r.PathValue("id"), input.Mode, input.ProjectIDs, actorID); err != nil {
		if errors.Is(err, postgres.ErrInvalidProjectScope) {
			jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_SCOPE", "指定项目不存在、跨租户或范围模式不匹配")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存资源项目范围失败")
		return
	}
	scope, err := h.projects.GetResourceScope(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取资源项目范围失败")
		return
	}
	if actor != nil && actor.Principal != nil {
		h.writeNotificationAudit(r.Context(), "project.assigned", r.PathValue("id"), "资源可用项目已更新", map[string]any{
			"mode": input.Mode, "project_ids": scope.ProjectIDs, "reason": strings.TrimSpace(input.Reason),
		}, actor.Principal.ID, string(actor.Principal.Kind))
	}
	jsonResp(w, http.StatusOK, scope)
}

func (h *Handler) GetResourceConsumerProjects(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	resourceID := r.PathValue("id")
	if resource, err := h.resources.GetByID(r.Context(), resourceID); err != nil || resource == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	projectIDs, err := h.projects.ListResourceConsumers(r.Context(), resourceID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询资源可使用项目失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"project_ids": projectIDs})
}

func (h *Handler) UpdateResourceConsumerProjects(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	ctx := r.Context()
	resourceID := r.PathValue("id")
	resource, err := h.resources.GetByID(ctx, resourceID)
	if err != nil || resource == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	if resource.ResourceType != "jenkins_job" {
		jsonError(w, http.StatusBadRequest, "RESOURCE_NOT_SHAREABLE", "当前仅 Jenkins Job 支持多项目使用")
		return
	}
	allowedProjects := map[string]bool{}
	if resource.IntegrationID != "" {
		integration, err := h.integrations.GetByID(ctx, resource.IntegrationID)
		if err != nil || integration == nil {
			jsonError(w, http.StatusConflict, "INTEGRATION_NOT_FOUND", "Jenkins Job 关联的 Integration 不存在")
			return
		}
		if integration.ProjectID != "" {
			allowedProjects[integration.ProjectID] = true
		}
		for _, projectID := range integration.ProjectIDs {
			allowedProjects[projectID] = true
		}
	}
	var input resourceConsumerProjectsInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.ProjectIDs) > 100 {
		jsonError(w, http.StatusBadRequest, "INVALID_PROJECT_SCOPE", "可使用项目请求体无效")
		return
	}
	seen := map[string]bool{}
	projectIDs := make([]string, 0, len(input.ProjectIDs))
	for _, projectID := range input.ProjectIDs {
		if projectID == "" || seen[projectID] {
			continue
		}
		if project, err := h.projects.GetByID(ctx, projectID); err != nil || project == nil {
			jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在: "+projectID)
			return
		}
		if !allowedProjects[projectID] {
			jsonError(w, http.StatusBadRequest, "PROJECT_NOT_ALLOWED", "项目不在 Jenkins Integration 的允许服务项目中")
			return
		}
		seen[projectID] = true
		projectIDs = append(projectIDs, projectID)
	}
	if err := h.projects.ReplaceResourceConsumers(ctx, resourceID, projectIDs); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存资源可使用项目失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"project_ids": projectIDs})
}

// Projects group catalog resources. Assignment precedence:
// resource override > namespace rule > integration > connector (native
// resources only) > default. Integration resources never inherit the channel.

func decodeProjectBody(w http.ResponseWriter, r *http.Request) (name, description string, ok bool) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return "", "", false
	}
	name = strings.TrimSpace(body.Name)
	description = strings.TrimSpace(body.Description)
	if name == "" || len([]rune(name)) > 100 || len([]rune(description)) > 500 {
		jsonError(w, http.StatusBadRequest, "INVALID_PROJECT", "项目名称为必填且不超过 100 字，描述不超过 500 字")
		return "", "", false
	}
	return name, description, true
}

func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	ctx := r.Context()
	items, err := h.projects.List(ctx)
	if err == nil && h.projectMemberships != nil && h.projectMembershipEnforced(ctx) {
		identity := identityFromContext(ctx)
		if identity != nil && !identity.IsTenantAdministrator() && identity.Principal != nil {
			items, err = h.projectMemberships.ListForPrincipal(ctx, identity.Principal.ID)
		}
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询项目失败")
		return
	}
	if items == nil {
		items = []domain.Project{}
	}
	items, err = filterProjectsForIdentity(identityFromContext(r.Context()), items)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PROJECT_SCOPE_ERROR", "按身份过滤项目失败")
		return
	}
	jsonResp(w, http.StatusOK, items)
}

func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	name, description, ok := decodeProjectBody(w, r)
	if !ok {
		return
	}
	project := &domain.Project{Name: name, Description: description}
	if err := h.projects.Create(r.Context(), project); err != nil {
		jsonError(w, http.StatusConflict, "PROJECT_NAME_CONFLICT", "项目名称已存在")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.created", project.ID, "项目已创建: "+project.Name, map[string]any{"name": project.Name}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusCreated, project)
}

func (h *Handler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	name, description, ok := decodeProjectBody(w, r)
	if !ok {
		return
	}
	project := &domain.Project{ID: r.PathValue("id"), Name: name, Description: description}
	if err := h.projects.Update(r.Context(), project); err != nil {
		if errors.Is(err, postgres.ErrDefaultProjectProtected) {
			jsonError(w, http.StatusConflict, "PROJECT_PROTECTED", "项目不存在或默认项目不可修改")
			return
		}
		jsonError(w, http.StatusConflict, "PROJECT_NAME_CONFLICT", "项目名称已存在")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.updated", project.ID, "项目已更新: "+project.Name, map[string]any{"name": project.Name}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusOK, project)
}

func (h *Handler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	id := r.PathValue("id")
	deleted, err := h.projects.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrProjectInUse) {
			jsonError(w, http.StatusConflict, "PROJECT_IN_USE", "项目已有执行、审批或审计历史，不能删除")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "删除项目失败")
		return
	}
	if !deleted {
		jsonError(w, http.StatusConflict, "PROJECT_PROTECTED", "项目不存在或默认项目不可删除")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.deleted", id, "项目已删除，相关资源回落到默认项目", map[string]any{}, actor.Principal.ID, string(actor.Principal.Kind))
	w.WriteHeader(http.StatusNoContent)
}

// SetConnectorProject assigns one connector (and all resources it discovers)
// to a project. Passing an empty project_id falls back to the default project.
func (h *Handler) SetConnectorProject(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	var body struct {
		ProjectID string `json:"project_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	body.ProjectID = strings.TrimSpace(body.ProjectID)
	if body.ProjectID != "" {
		project, err := h.projects.GetByID(r.Context(), body.ProjectID)
		if err != nil || project == nil {
			jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在")
			return
		}
	}
	connectorID := r.PathValue("id")
	updated, err := h.projects.SetConnectorProject(r.Context(), connectorID, body.ProjectID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新执行通道项目失败")
		return
	}
	if !updated {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "执行通道不存在")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.assigned", connectorID, "执行通道项目归属已更新", map[string]any{"project_id": body.ProjectID}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusOK, map[string]string{"connector_id": connectorID, "project_id": body.ProjectID})
}

// SetConnectorEnvironment updates the execution channel as a governed
// resource. Native resources and Integrations without an environment override
// resolve this value dynamically, so no child rows need to be rewritten.
func (h *Handler) SetConnectorEnvironment(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	var body struct {
		Environment string `json:"environment"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	body.Environment = strings.TrimSpace(body.Environment)
	if !validEnvironment(body.Environment, false) {
		jsonError(w, http.StatusBadRequest, "INVALID_ENVIRONMENT", "环境必须是 development、testing 或 production")
		return
	}
	connectorID := r.PathValue("id")
	updated, err := h.projects.SetConnectorEnvironment(r.Context(), connectorID, body.Environment)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新执行通道环境失败")
		return
	}
	if !updated {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "执行通道不存在")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "connector.environment.updated", connectorID, "执行通道环境已更新",
		map[string]any{"environment": body.Environment}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusOK, map[string]string{"connector_id": connectorID, "environment": body.Environment})
}

func (h *Handler) SetResourceProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
		Reason    string `json:"reason"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	body.ProjectID, body.Reason = strings.TrimSpace(body.ProjectID), strings.TrimSpace(body.Reason)
	if body.ProjectID == "" || len([]rune(body.Reason)) > 500 {
		jsonError(w, http.StatusBadRequest, "INVALID_OVERRIDE", "项目为必填，原因不能超过 500 字")
		return
	}
	resourceID := r.PathValue("id")
	resource, err := h.resources.GetByID(r.Context(), resourceID)
	if err != nil || resource == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	project, err := h.projects.GetByID(r.Context(), body.ProjectID)
	if err != nil || project == nil {
		jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在")
		return
	}
	actor := identityFromContext(r.Context())
	if err := h.projects.SetResourceProject(r.Context(), resourceID, body.ProjectID, actor.Principal.ID, body.Reason); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新资源项目失败")
		return
	}
	h.writeNotificationAudit(r.Context(), "project.assigned", resourceID, "资源项目归属已覆盖: "+resource.Name,
		map[string]any{"project_id": body.ProjectID, "resource_id": resourceID, "reason": body.Reason, "source": "RESOURCE"}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusOK, map[string]string{"resource_id": resourceID, "project_id": body.ProjectID, "project_name": project.Name, "project_source": "RESOURCE"})
}

func (h *Handler) DeleteResourceProject(w http.ResponseWriter, r *http.Request) {
	resourceID := r.PathValue("id")
	resource, err := h.resources.GetByID(r.Context(), resourceID)
	if err != nil || resource == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	deleted, err := h.projects.DeleteResourceProject(r.Context(), resourceID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "恢复资源继承失败")
		return
	}
	if !deleted {
		jsonError(w, http.StatusNotFound, "OVERRIDE_NOT_FOUND", "资源没有单独覆盖规则")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.assigned", resourceID, "资源已恢复继承: "+resource.Name,
		map[string]any{"resource_id": resourceID, "source": "INHERITED"}, actor.Principal.ID, string(actor.Principal.Kind))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) SetResourceEnvironment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Environment string `json:"environment"`
		Reason      string `json:"reason"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	body.Environment, body.Reason = strings.TrimSpace(body.Environment), strings.TrimSpace(body.Reason)
	if !validEnvironment(body.Environment, false) || len([]rune(body.Reason)) > 500 {
		jsonError(w, http.StatusBadRequest, "INVALID_ENVIRONMENT", "环境必须是 development、testing 或 production，原因不能超过 500 字")
		return
	}
	resourceID := r.PathValue("id")
	resource, err := h.resources.GetByID(r.Context(), resourceID)
	if err != nil || resource == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	actor := identityFromContext(r.Context())
	if err := h.projects.SetResourceEnvironment(r.Context(), resourceID, body.Environment, actor.Principal.ID, body.Reason); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新资源环境失败")
		return
	}
	h.writeNotificationAudit(r.Context(), "resource.environment.assigned", resourceID, "资源环境归属已覆盖: "+resource.Name,
		map[string]any{"resource_id": resourceID, "environment": body.Environment, "reason": body.Reason, "source": "RESOURCE"}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusOK, map[string]string{"resource_id": resourceID, "environment": body.Environment, "environment_source": "RESOURCE"})
}

func (h *Handler) DeleteResourceEnvironment(w http.ResponseWriter, r *http.Request) {
	resourceID := r.PathValue("id")
	resource, err := h.resources.GetByID(r.Context(), resourceID)
	if err != nil || resource == nil {
		jsonError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "资源不存在")
		return
	}
	deleted, err := h.projects.DeleteResourceEnvironment(r.Context(), resourceID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "恢复资源环境继承失败")
		return
	}
	if !deleted {
		jsonError(w, http.StatusNotFound, "OVERRIDE_NOT_FOUND", "资源没有单独环境覆盖")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "resource.environment.inheritance_restored", resourceID, "资源环境已恢复继承: "+resource.Name,
		map[string]any{"resource_id": resourceID, "environment_source": "INHERITED"}, actor.Principal.ID, string(actor.Principal.Kind))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListProjectNamespaceRules(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	items, err := h.projects.ListNamespaceRules(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询项目命名空间规则失败")
		return
	}
	if items == nil {
		items = []domain.ProjectNamespaceRule{}
	}
	jsonResp(w, http.StatusOK, items)
}

func (h *Handler) UpsertProjectNamespaceRule(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	var body struct {
		ProjectID   string `json:"project_id"`
		ConnectorID string `json:"connector_id"`
		Namespace   string `json:"namespace"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	body.ProjectID, body.ConnectorID = strings.TrimSpace(body.ProjectID), strings.TrimSpace(body.ConnectorID)
	body.Namespace = strings.TrimSpace(body.Namespace)
	if body.ProjectID == "" || body.ConnectorID == "" || body.Namespace == "" || len(body.Namespace) > 255 {
		jsonError(w, http.StatusBadRequest, "INVALID_RULE", "项目、执行通道和命名空间为必填")
		return
	}
	project, err := h.projects.GetByID(r.Context(), body.ProjectID)
	if err != nil || project == nil {
		jsonError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "项目不存在")
		return
	}
	connector, err := h.connectors.GetByID(r.Context(), body.ConnectorID)
	if err != nil || connector == nil {
		jsonError(w, http.StatusNotFound, "CONNECTOR_NOT_FOUND", "执行通道不存在")
		return
	}
	rule := &domain.ProjectNamespaceRule{ProjectID: body.ProjectID, ConnectorID: body.ConnectorID, Namespace: body.Namespace}
	if err := h.projects.UpsertNamespaceRule(r.Context(), rule); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存项目命名空间规则失败")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.assigned", rule.ID, "命名空间项目归属已更新: "+body.Namespace, map[string]any{
		"project_id": body.ProjectID, "connector_id": body.ConnectorID, "namespace": body.Namespace,
	}, actor.Principal.ID, string(actor.Principal.Kind))
	jsonResp(w, http.StatusOK, rule)
}

func (h *Handler) DeleteProjectNamespaceRule(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		databaseUnavailable(w)
		return
	}
	id := r.PathValue("id")
	deleted, err := h.projects.DeleteNamespaceRule(r.Context(), id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "删除项目命名空间规则失败")
		return
	}
	if !deleted {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", "规则不存在")
		return
	}
	actor := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "project.assigned", id, "命名空间项目归属规则已删除", map[string]any{}, actor.Principal.ID, string(actor.Principal.Kind))
	w.WriteHeader(http.StatusNoContent)
}
