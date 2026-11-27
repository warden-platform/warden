package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Tool is an MCP tool definition plus its in-process handler.
type Tool struct {
	Name        string                                             `json:"name"`
	Title       string                                             `json:"title,omitempty"`
	Description string                                             `json:"description"`
	InputSchema map[string]any                                     `json:"inputSchema"`
	Annotations map[string]any                                     `json:"annotations,omitempty"`
	Handler     func(context.Context, map[string]any) (any, error) `json:"-"`
}

type ToolError struct {
	Code    string
	Message string
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

const (
	scopeResourcesRead = "warden:resources:read"
	scopeExecutionRead = "warden:executions:read:self"
	defaultListLimit   = 20
	maxListLimit       = 50
	defaultWaitSeconds = int32(30)
	maxWaitLowerBound  = int32(1)
)

var readOnlyAnnotations = map[string]any{
	"readOnlyHint":    true,
	"destructiveHint": false,
	"idempotentHint":  true,
	"openWorldHint":   false,
}

// ReadOnlyTools returns the tools visible to a Principal with the given
// capabilities. No write or approval tools are exposed in this version.
func ReadOnlyTools(client *Client, capabilities []string, maxWait time.Duration) []Tool {
	hasResources := hasCapability(capabilities, scopeResourcesRead)
	hasExecutionRead := hasCapability(capabilities, scopeExecutionRead)
	maxWaitSeconds := int32(maxWait / time.Second)
	if maxWaitSeconds < maxWaitLowerBound {
		maxWaitSeconds = 60
	}

	tools := make([]Tool, 0, 4)
	if hasResources {
		tools = append(tools,
			Tool{
				Name:        "warden_list_resources",
				Title:       "List Warden resources",
				Description: "List Warden resources visible to this agent. Read-only. Prefer query/project/resource_type filters to keep the result small; metadata and credentials are never returned.",
				InputSchema: objectSchema(map[string]any{
					"query":         map[string]any{"type": "string", "description": "Optional case-insensitive search over resource name, external ID and namespace."},
					"project_id":    map[string]any{"type": "string", "description": "Optional Warden project ID."},
					"environment":   map[string]any{"type": "string", "description": "Optional environment filter, e.g. production."},
					"resource_type": map[string]any{"type": "string", "description": "Optional resource type filter, e.g. kubernetes.deployment."},
					"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": maxListLimit, "default": defaultListLimit},
					"offset":        map[string]any{"type": "integer", "minimum": 0, "default": 0},
				}, nil),
				Annotations: readOnlyAnnotations,
				Handler:     listResourcesHandler(client),
			},
			Tool{
				Name:        "warden_list_resource_actions",
				Title:       "List resource actions",
				Description: "List the structured actions available on one Warden resource, including the parameter JSON Schema, risk level and Connector availability. Read-only.",
				InputSchema: objectSchema(map[string]any{
					"resource_id": map[string]any{"type": "string", "description": "Warden resource ID returned by warden_list_resources."},
				}, []string{"resource_id"}),
				Annotations: readOnlyAnnotations,
				Handler:     listResourceActionsHandler(client),
			},
		)
	}
	if hasExecutionRead {
		tools = append(tools,
			Tool{
				Name:        "warden_get_execution",
				Title:       "Get execution status",
				Description: "Read the current status and sanitized result of a Warden execution by ID. Read-only; only executions owned by this agent are visible.",
				InputSchema: objectSchema(map[string]any{
					"execution_id": map[string]any{"type": "string", "description": "Warden execution ID."},
				}, []string{"execution_id"}),
				Annotations: readOnlyAnnotations,
				Handler:     getExecutionHandler(client),
			},
			Tool{
				Name:        "warden_wait_execution",
				Title:       "Wait for execution",
				Description: "Poll a Warden execution until it reaches a terminal state or the bounded wait expires. Read-only. Use warden_get_execution for a single read.",
				InputSchema: objectSchema(map[string]any{
					"execution_id": map[string]any{"type": "string", "description": "Warden execution ID."},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     maxWaitSeconds,
						"default":     defaultWaitSeconds,
						"description": "Bounded wait in seconds; the adapter caps it with WARDEN_MCP_MAX_WAIT_SECONDS.",
					},
				}, []string{"execution_id"}),
				Annotations: readOnlyAnnotations,
				Handler:     waitExecutionHandler(client, maxWait),
			},
		)
	}
	return tools
}

func hasCapability(capabilities []string, required string) bool {
	for _, capability := range capabilities {
		if strings.TrimSpace(capability) == required {
			return true
		}
	}
	return false
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	obj := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	return obj
}

type listResourcesInput struct {
	Query        string `json:"query"`
	ProjectID    string `json:"project_id"`
	Environment  string `json:"environment"`
	ResourceType string `json:"resource_type"`
	Limit        int    `json:"limit"`
	Offset       int    `json:"offset"`
}

type resourceSummary struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	ResourceType         string `json:"resource_type"`
	ExternalID           string `json:"external_id,omitempty"`
	Namespace            string `json:"namespace,omitempty"`
	ProjectID            string `json:"project_id,omitempty"`
	ProjectName          string `json:"project_name,omitempty"`
	Environment          string `json:"environment,omitempty"`
	Status               string `json:"status"`
	ConnectorID          string `json:"connector_id,omitempty"`
	AvailableActionCount int    `json:"available_action_count"`
}

func listResourcesHandler(client *Client) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, args map[string]any) (any, error) {
		var input listResourcesInput
		if err := decodeArgs(args, &input); err != nil {
			return nil, invalidArgument("warden_list_resources 参数格式无效")
		}
		if input.Limit == 0 {
			input.Limit = defaultListLimit
		}
		if input.Limit < 1 || input.Limit > maxListLimit {
			return nil, invalidArgument(fmt.Sprintf("limit 必须在 1-%d 之间", maxListLimit))
		}
		if input.Offset < 0 {
			return nil, invalidArgument("offset 不能为负数")
		}

		resources, err := client.ListResources(ctx, ListResourcesOptions{
			Query:        strings.TrimSpace(input.Query),
			ProjectID:    strings.TrimSpace(input.ProjectID),
			Environment:  strings.TrimSpace(input.Environment),
			ResourceType: strings.TrimSpace(input.ResourceType),
			Limit:        input.Limit,
			Offset:       input.Offset,
		})
		if err != nil {
			return nil, toolErrorFrom(err)
		}

		items := make([]resourceSummary, 0, len(resources))
		for _, resource := range resources {
			items = append(items, resourceSummary{
				ID:                   resource.ID,
				Name:                 resource.Name,
				ResourceType:         resource.ResourceType,
				ExternalID:           resource.ExternalID,
				Namespace:            resource.Namespace,
				ProjectID:            resource.ProjectID,
				ProjectName:          resource.ProjectName,
				Environment:          resource.Environment,
				Status:               resource.Status,
				ConnectorID:          resource.ConnectorID,
				AvailableActionCount: resource.AvailableActionCount,
			})
		}
		result := map[string]any{
			"resources": items,
			"returned":  len(items),
		}
		if len(items) == input.Limit {
			result["next_offset"] = input.Offset + len(items)
		}
		return result, nil
	}
}

type listActionsInput struct {
	ResourceID string `json:"resource_id"`
}

type actionSummary struct {
	Name               string         `json:"name"`
	Version            int            `json:"version"`
	DisplayName        string         `json:"display_name,omitempty"`
	Description        string         `json:"description,omitempty"`
	RiskLevel          string         `json:"risk_level,omitempty"`
	ExecutionMode      string         `json:"execution_mode,omitempty"`
	TimeoutSeconds     int            `json:"timeout_seconds,omitempty"`
	ParameterSchema    map[string]any `json:"parameter_schema"`
	Available          bool           `json:"available"`
	AvailabilityCode   string         `json:"availability_code,omitempty"`
	AvailabilityReason string         `json:"availability_reason,omitempty"`
	RequiredCapability string         `json:"required_capability,omitempty"`
}

func listResourceActionsHandler(client *Client) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, args map[string]any) (any, error) {
		var input listActionsInput
		if err := decodeArgs(args, &input); err != nil || strings.TrimSpace(input.ResourceID) == "" {
			return nil, invalidArgument("resource_id 为必填")
		}
		actions, err := client.ListResourceActions(ctx, strings.TrimSpace(input.ResourceID))
		if err != nil {
			return nil, toolErrorFrom(err)
		}
		items := make([]actionSummary, 0, len(actions))
		for _, action := range actions {
			items = append(items, actionSummary{
				Name:               action.Name,
				Version:            action.Version,
				DisplayName:        action.DisplayName,
				Description:        action.Description,
				RiskLevel:          action.RiskLevel,
				ExecutionMode:      action.ExecutionMode,
				TimeoutSeconds:     action.TimeoutSeconds,
				ParameterSchema:    action.ParameterSchema,
				Available:          action.Available,
				AvailabilityCode:   action.AvailabilityCode,
				AvailabilityReason: action.AvailabilityReason,
				RequiredCapability: action.RequiredCapability,
			})
		}
		return map[string]any{"resource_id": input.ResourceID, "actions": items}, nil
	}
}

type executionInput struct {
	ExecutionID string `json:"execution_id"`
}

type waitExecutionInput struct {
	ExecutionID    string `json:"execution_id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type executionSummary struct {
	ID                string         `json:"execution_id"`
	Status            string         `json:"status"`
	Terminal          bool           `json:"terminal"`
	ActionName        string         `json:"action_name,omitempty"`
	ActionDisplayName string         `json:"action_display_name,omitempty"`
	RiskLevel         string         `json:"risk_level,omitempty"`
	ResourceID        string         `json:"resource_id,omitempty"`
	ResourceName      string         `json:"resource_name,omitempty"`
	ResourceType      string         `json:"resource_type,omitempty"`
	TraceID           string         `json:"trace_id,omitempty"`
	ErrorCode         string         `json:"error_code,omitempty"`
	ErrorMessage      string         `json:"error_message,omitempty"`
	Result            map[string]any `json:"result,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	NextPollSeconds   int            `json:"next_poll_seconds,omitempty"`
	Message           string         `json:"message,omitempty"`
}

func summaryFromExecution(execution *Execution) executionSummary {
	return executionSummary{
		ID:                execution.ID,
		Status:            execution.Status,
		Terminal:          IsTerminal(execution.Status),
		ActionName:        execution.ActionName,
		ActionDisplayName: execution.ActionDisplayName,
		RiskLevel:         execution.RiskLevel,
		ResourceID:        execution.ResourceID,
		ResourceName:      execution.ResourceName,
		ResourceType:      execution.ResourceType,
		TraceID:           execution.TraceID,
		ErrorCode:         execution.ErrorCode,
		ErrorMessage:      execution.ErrorMessage,
		Result:            execution.Result,
		CreatedAt:         execution.CreatedAt,
		UpdatedAt:         execution.UpdatedAt,
	}
}

func getExecutionHandler(client *Client) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, args map[string]any) (any, error) {
		var input executionInput
		if err := decodeArgs(args, &input); err != nil || strings.TrimSpace(input.ExecutionID) == "" {
			return nil, invalidArgument("execution_id 为必填")
		}
		execution, err := client.GetExecution(ctx, strings.TrimSpace(input.ExecutionID))
		if err != nil {
			return nil, toolErrorFrom(err)
		}
		return summaryFromExecution(execution), nil
	}
}

func waitExecutionHandler(client *Client, maxWait time.Duration) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, args map[string]any) (any, error) {
		var input waitExecutionInput
		if err := decodeArgs(args, &input); err != nil || strings.TrimSpace(input.ExecutionID) == "" {
			return nil, invalidArgument("execution_id 为必填")
		}
		if maxWait <= 0 {
			maxWait = 60 * time.Second
		}
		timeout := defaultWaitSeconds
		if input.TimeoutSeconds > 0 {
			timeout = int32(input.TimeoutSeconds)
		}
		maxSeconds := int32(maxWait / time.Second)
		if maxSeconds < maxWaitLowerBound {
			maxSeconds = 60
		}
		if timeout > maxSeconds {
			timeout = maxSeconds
		}
		if timeout < maxWaitLowerBound {
			timeout = maxWaitLowerBound
		}

		execution, err := client.WaitExecution(ctx, strings.TrimSpace(input.ExecutionID), time.Duration(timeout)*time.Second, 0)
		if err != nil {
			return nil, toolErrorFrom(err)
		}
		summary := summaryFromExecution(execution)
		if !summary.Terminal {
			summary.NextPollSeconds = 5
			summary.Message = "执行尚未结束；可再次调用 warden_wait_execution 或稍后用 warden_get_execution 查询。"
		}
		return summary, nil
	}
}

func decodeArgs(args map[string]any, out any) error {
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

func invalidArgument(message string) error {
	return &ToolError{Code: "INVALID_ARGUMENT", Message: message}
}

func toolErrorFrom(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		code := apiErr.Code
		if code == "" {
			code = "UNAVAILABLE"
		}
		return &ToolError{Code: code, Message: apiErr.Message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ToolError{Code: "TIMEOUT", Message: "等待 Warden API 超时"}
	}
	if errors.Is(err, context.Canceled) {
		return &ToolError{Code: "CANCELLED", Message: "请求已取消"}
	}
	return &ToolError{Code: "UNAVAILABLE", Message: "Warden API 调用失败"}
}
