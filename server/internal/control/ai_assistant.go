package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"gitee.com/warden-platform/warden/server/internal/ai"
	"gitee.com/warden-platform/warden/server/internal/domain"
	"gitee.com/warden-platform/warden/server/internal/storage/postgres"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

const maxAIHistoryMessages = 20

type aiChatRequest struct {
	ConversationID string `json:"conversationId"`
	Message        string `json:"message"`
}

type aiStreamRunner interface {
	Run(context.Context, []*schema.Message, string, func(ai.Event) error) error
}

var newAIStreamRunner = func(ctx context.Context, cfg *ai.Config, tools []tool.BaseTool) (aiStreamRunner, error) {
	return ai.NewRunner(ctx, cfg, tools)
}

func (h *Handler) AIChat(w http.ResponseWriter, r *http.Request) {
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "请先登录")
		return
	}
	if h.aiStore == nil {
		databaseUnavailable(w)
		return
	}
	if h.aiConfig != nil && !h.aiConfig.Enabled {
		jsonError(w, http.StatusServiceUnavailable, "AI_DISABLED", "AI 助手已停用")
		return
	}
	if !h.aiConfig.Configured() {
		jsonError(w, http.StatusServiceUnavailable, "AI_NOT_CONFIGURED", "AI 模型尚未配置，请设置 WARDEN_AI_MODEL 和 WARDEN_AI_API_KEY")
		return
	}
	var request aiChatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&request); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST", "请求格式无效")
		return
	}
	request.Message = strings.TrimSpace(request.Message)
	if request.Message == "" || utf8.RuneCountInString(request.Message) > 4000 {
		jsonError(w, http.StatusBadRequest, "INVALID_MESSAGE", "消息不能为空且不能超过 4000 个字符")
		return
	}

	conversationID := strings.TrimSpace(request.ConversationID)
	if conversationID == "" {
		conversation, err := h.aiStore.CreateConversation(r.Context(), identity.Principal.ID, aiConversationTitle(request.Message))
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "AI_STORE_ERROR", "创建 AI 会话失败")
			return
		}
		conversationID = conversation.ID
	} else {
		if _, err := uuid.Parse(conversationID); err != nil {
			jsonError(w, http.StatusBadRequest, "INVALID_CONVERSATION", "会话标识无效")
			return
		}
		conversation, err := h.aiStore.GetConversation(r.Context(), conversationID, identity.Principal.ID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "AI_STORE_ERROR", "查询 AI 会话失败")
			return
		}
		if conversation == nil {
			jsonError(w, http.StatusNotFound, "AI_CONVERSATION_NOT_FOUND", "会话不存在或无权访问")
			return
		}
	}

	historyRecords, err := h.aiStore.ListMessages(r.Context(), conversationID, identity.Principal.ID, maxAIHistoryMessages)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "AI_STORE_ERROR", "读取 AI 会话失败")
		return
	}
	if _, err := h.aiStore.AddMessage(r.Context(), conversationID, "", "user", request.Message); err != nil {
		jsonError(w, http.StatusInternalServerError, "AI_STORE_ERROR", "保存用户消息失败")
		return
	}
	run, err := h.aiStore.CreateRun(r.Context(), conversationID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "AI_STORE_ERROR", "创建 AI 运行记录失败")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = h.aiStore.FinishRun(r.Context(), run.ID, "FAILED", "STREAMING_UNSUPPORTED")
		jsonError(w, http.StatusInternalServerError, "STREAMING_UNSUPPORTED", "当前服务不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	writeAIEvent(w, "meta", map[string]string{"conversationId": conversationID, "runId": run.ID})
	flusher.Flush()

	history := make([]*schema.Message, 0, len(historyRecords))
	for _, message := range historyRecords {
		switch message.Role {
		case "user":
			history = append(history, schema.UserMessage(message.Content))
		case "assistant":
			history = append(history, schema.AssistantMessage(message.Content, nil))
		}
	}
	tools, err := h.aiTools()
	if err != nil {
		h.finishAIWithError(w, r, run.ID, "AI_TOOL_ERROR", err)
		return
	}
	runner, err := newAIStreamRunner(r.Context(), h.aiConfig, tools)
	if err != nil {
		h.finishAIWithError(w, r, run.ID, "AI_MODEL_ERROR", err)
		return
	}
	var answer strings.Builder
	err = runner.Run(r.Context(), history, request.Message, func(event ai.Event) error {
		switch event.Kind {
		case "delta":
			answer.WriteString(event.Content)
			writeAIEvent(w, "delta", map[string]string{"content": event.Content})
		case "tool":
			status := "COMPLETED"
			output := event.Content
			if len(output) > 32*1024 {
				output = output[:32*1024]
			}
			if storeErr := h.aiStore.AddToolCall(r.Context(), &domain.AIToolCall{
				RunID: run.ID, ProviderID: event.ToolCallID, ToolName: event.ToolName, Status: status, Output: output,
			}); storeErr != nil {
				return storeErr
			}
			writeAIEvent(w, "tool", map[string]string{"name": event.ToolName, "status": "completed"})
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		log.Printf("AI run %s failed: %v", run.ID, err)
		h.finishAIWithError(w, r, run.ID, "AI_RUN_FAILED", errors.New("模型调用或工具执行失败"))
		return
	}
	if answer.Len() == 0 {
		h.finishAIWithError(w, r, run.ID, "AI_EMPTY_RESPONSE", errors.New("模型未返回有效回答"))
		return
	}
	if _, err := h.aiStore.AddMessage(r.Context(), conversationID, run.ID, "assistant", answer.String()); err != nil {
		h.finishAIWithError(w, r, run.ID, "AI_STORE_ERROR", err)
		return
	}
	if err := h.aiStore.FinishRun(r.Context(), run.ID, "COMPLETED", ""); err != nil {
		h.finishAIWithError(w, r, run.ID, "AI_STORE_ERROR", err)
		return
	}
	writeAIEvent(w, "done", map[string]string{"runId": run.ID})
	flusher.Flush()
}

func (h *Handler) finishAIWithError(w http.ResponseWriter, r *http.Request, runID, code string, err error) {
	_ = h.aiStore.FinishRun(r.Context(), runID, "FAILED", code)
	writeAIEvent(w, "error", map[string]string{"error": code, "message": err.Error()})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeAIEvent(w http.ResponseWriter, name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
}

func aiConversationTitle(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > 60 {
		return string(runes[:60])
	}
	return string(runes)
}

type aiResourceQuery struct {
	Query     string `json:"query,omitempty" jsonschema:"description=按名称、命名空间或外部标识模糊搜索"`
	Type      string `json:"type,omitempty" jsonschema:"description=资源类型，例如 kubernetes_deployment 或 linux_host"`
	Domain    string `json:"domain,omitempty" jsonschema:"description=资源域，例如 kubernetes、linux、jenkins、database、huawei、network"`
	ProjectID string `json:"projectId,omitempty" jsonschema:"description=可选的 Warden 项目 ID"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=最多返回多少条，默认 10，最大 20"`
}

type aiExecutionQuery struct {
	Status    string `json:"status,omitempty" jsonschema:"description=执行状态，例如 FAILED、SUCCEEDED、RUNNING"`
	ProjectID string `json:"projectId,omitempty" jsonschema:"description=可选的 Warden 项目 ID"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=最多返回多少条，默认 10，最大 20"`
}

type aiAuditQuery struct {
	Action      string `json:"action,omitempty" jsonschema:"description=审计动作，例如 execution.created"`
	ExecutionID string `json:"executionId,omitempty" jsonschema:"description=关联执行 ID"`
	TraceID     string `json:"traceId,omitempty" jsonschema:"description=链路追踪 ID"`
	Query       string `json:"query,omitempty" jsonschema:"description=审计摘要或字段搜索词"`
	Limit       int    `json:"limit,omitempty" jsonschema:"description=最多返回多少条，默认 10，最大 20"`
}

func (h *Handler) aiTools() ([]tool.BaseTool, error) {
	resourceTool, err := utils.InferTool("query_resources", "查询当前用户有权访问的 Warden 资源，只读，不返回凭证。", h.queryAIResources)
	if err != nil {
		return nil, err
	}
	executionTool, err := utils.InferTool("query_executions", "查询并分析当前用户可见的执行记录及失败信息，只读。", h.queryAIExecutions)
	if err != nil {
		return nil, err
	}
	auditTool, err := utils.InferTool("query_audit_events", "查询当前用户有权限查看的审计事件，只读。", h.queryAIAuditEvents)
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{resourceTool, executionTool, auditTool}, nil
}

func (h *Handler) queryAIResources(ctx context.Context, input *aiResourceQuery) (string, error) {
	identity := identityFromContext(ctx)
	if !identity.HasScope("warden:resources:read") {
		return "", errors.New("当前身份没有资源查询权限")
	}
	if input.Domain != "" && !validAIResourceDomain(input.Domain) {
		return "", errors.New("资源域参数无效")
	}
	resources, err := h.resources.List(ctx, input.Type, input.Domain, "", "", "", 200, 0)
	if err != nil {
		return "", errors.New("查询资源失败")
	}
	if err := h.resources.EnrichProjectScopes(ctx, resources); err != nil {
		return "", errors.New("查询资源项目范围失败")
	}
	if projectID := strings.TrimSpace(input.ProjectID); projectID != "" {
		if err := h.projectAccess(ctx, projectID, false); err != nil {
			return "", errors.New("当前身份不能访问指定项目")
		}
		project, err := h.projects.GetByID(ctx, projectID)
		if err != nil || project == nil {
			return "", errors.New("项目不存在")
		}
		filtered := make([]domain.Resource, 0, len(resources))
		for _, resource := range resources {
			for _, allowedProjectID := range resource.ProjectIDs {
				if allowedProjectID == projectID {
					filtered = append(filtered, resource)
					break
				}
			}
		}
		resources = filtered
	}
	resources, err = h.filterResourcesForIdentity(ctx, identity, resources, input.ProjectID)
	if err != nil {
		return "", errors.New("校验资源项目权限失败")
	}
	resources, err = h.filterResourcesByProjectMembership(ctx, resources)
	if err != nil {
		return "", errors.New("校验项目成员权限失败")
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	result := make([]map[string]any, 0)
	for _, resource := range resources {
		if query != "" && !strings.Contains(strings.ToLower(resource.Name+" "+resource.Namespace+" "+resource.ExternalID), query) {
			continue
		}
		result = append(result, map[string]any{
			"id": resource.ID, "name": resource.Name, "type": resource.ResourceType,
			"namespace": resource.Namespace, "status": resource.Status, "environment": resource.Environment,
			"projectIds": resource.ProjectIDs, "projectNames": resource.ProjectNames,
		})
		if len(result) >= aiToolLimit(input.Limit) {
			break
		}
	}
	return marshalAIToolResult(result)
}

func (h *Handler) queryAIExecutions(ctx context.Context, input *aiExecutionQuery) (string, error) {
	identity := identityFromContext(ctx)
	if !identity.HasScope("warden:executions:read:self") {
		return "", errors.New("当前身份没有执行记录查询权限")
	}
	var executions []domain.Execution
	var err error
	if identity.IsPlatformAdmin && domain.TenantIDFromContext(ctx) == identity.TenantID {
		executions, err = h.executions.ListAllTenants(ctx, input.Status, 50, 0)
	} else {
		principalID := ""
		if !identity.IsTenantAdministrator() {
			principalID = identity.Principal.ID
		}
		executions, err = h.executions.List(ctx, principalID, input.Status, 50, 0)
	}
	if err != nil {
		return "", errors.New("查询执行记录失败")
	}
	executions, err = h.filterExecutionsForProject(ctx, identity, executions, input.ProjectID)
	if err != nil {
		return "", errors.New("校验执行项目权限失败")
	}
	h.enrichExecutions(ctx, executions)
	result := make([]map[string]any, 0, len(executions))
	for _, execution := range executions {
		result = append(result, map[string]any{
			"id": execution.ID, "status": execution.Status, "resourceName": execution.ResourceName,
			"actionName": execution.ActionName, "principalName": execution.PrincipalName,
			"errorCode": execution.ErrorCode, "errorMessage": execution.ErrorMessage,
			"traceId": execution.TraceID, "createdAt": execution.CreatedAt, "updatedAt": execution.UpdatedAt,
		})
		if len(result) >= aiToolLimit(input.Limit) {
			break
		}
	}
	return marshalAIToolResult(result)
}

func (h *Handler) queryAIAuditEvents(ctx context.Context, input *aiAuditQuery) (string, error) {
	if !identityFromContext(ctx).HasScope("warden:audit:read") {
		return "", errors.New("当前身份没有审计查询权限")
	}
	events, err := h.audit.List(ctx, postgres.AuditListFilter{
		Action: input.Action, ExecutionID: input.ExecutionID, TraceID: input.TraceID, Query: strings.TrimSpace(input.Query),
	}, 50, 0)
	if err != nil {
		return "", errors.New("查询审计事件失败")
	}
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		result = append(result, map[string]any{
			"id": event.ID, "action": event.Action, "actorId": event.ActorID,
			"resourceId": event.ResourceID, "executionId": event.ExecutionID,
			"traceId": event.TraceID, "summary": event.Summary, "createdAt": event.CreatedAt,
		})
		if len(result) >= aiToolLimit(input.Limit) {
			break
		}
	}
	return marshalAIToolResult(result)
}

func aiToolLimit(limit int) int {
	if limit < 1 {
		return 10
	}
	if limit > 20 {
		return 20
	}
	return limit
}

func marshalAIToolResult(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("整理工具结果失败")
	}
	return string(data), nil
}

func validAIResourceDomain(value string) bool {
	switch value {
	case "kubernetes", "linux", "jenkins", "grafana", "database", "huawei", "network":
		return true
	default:
		return false
	}
}
