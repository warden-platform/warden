package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 16 << 20

// APIError is a sanitized error returned by the Warden REST API.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("warden api %d %s: %s", e.Status, e.Code, e.Message)
}

// Client is a minimal read-only Warden REST client used by the MCP adapter.
type Client struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	pollInterval time.Duration
}

func NewClient(endpoint, apiKey string, timeout time.Duration) (*Client, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("WARDEN_API_ENDPOINT 不能为空")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("WARDEN_API_ENDPOINT 不是合法 URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("WARDEN_API_ENDPOINT 只支持 http/https")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("WARDEN_AGENT_API_KEY 不能为空")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL:      endpoint,
		apiKey:       apiKey,
		httpClient:   &http.Client{Timeout: timeout},
		pollInterval: 2 * time.Second,
	}, nil
}

// SetPollInterval adjusts the polling cadence used by WaitExecution.
func (c *Client) SetPollInterval(interval time.Duration) {
	if interval > 0 {
		c.pollInterval = interval
	}
}

type Identity struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Scopes       []string `json:"scopes"`
	Capabilities []string `json:"capabilities"`
}

func (c *Client) Me(ctx context.Context) (*Identity, error) {
	var identity Identity
	if err := c.get(ctx, "/auth/me", nil, &identity); err != nil {
		return nil, err
	}
	if len(identity.Capabilities) == 0 {
		identity.Capabilities = identity.Scopes
	}
	return &identity, nil
}

type Resource struct {
	ID                   string `json:"id"`
	ResourceType         string `json:"resource_type"`
	ExternalID           string `json:"external_id"`
	Name                 string `json:"name"`
	Namespace            string `json:"namespace"`
	ConnectorID          string `json:"connector_id"`
	ProjectID            string `json:"project_id"`
	ProjectName          string `json:"project_name"`
	Environment          string `json:"environment"`
	Status               string `json:"status"`
	AvailableActionCount int    `json:"available_action_count"`
}

type ListResourcesOptions struct {
	Query        string
	ProjectID    string
	Environment  string
	ResourceType string
	Limit        int
	Offset       int
}

// ListResources returns a bounded page of resources. Query/environment filters
// are applied client-side because the REST endpoint does not expose them; those
// filtered lookups scan at most 1000 resources.
func (c *Client) ListResources(ctx context.Context, opts ListResourcesOptions) ([]Resource, error) {
	query := url.Values{}
	if opts.ResourceType != "" {
		query.Set("type", opts.ResourceType)
	}
	if opts.ProjectID != "" {
		query.Set("projectId", opts.ProjectID)
	}

	if opts.Query != "" || opts.Environment != "" {
		query.Set("limit", "1000")
		query.Set("offset", "0")
		var all []Resource
		if err := c.get(ctx, "/resources", query, &all); err != nil {
			return nil, err
		}
		filtered := make([]Resource, 0, len(all))
		for _, resource := range all {
			if !matchesResourceQuery(resource, opts.Query) {
				continue
			}
			if opts.Environment != "" && !strings.EqualFold(resource.Environment, opts.Environment) {
				continue
			}
			filtered = append(filtered, resource)
		}
		return sliceResources(filtered, opts.Offset, opts.Limit), nil
	}

	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Offset > 0 {
		query.Set("offset", strconv.Itoa(opts.Offset))
	}
	var resources []Resource
	if err := c.get(ctx, "/resources", query, &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

func matchesResourceQuery(resource Resource, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	values := []string{resource.Name, resource.ExternalID, resource.Namespace}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func sliceResources(resources []Resource, offset, limit int) []Resource {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(resources) {
		return []Resource{}
	}
	end := offset + limit
	if limit <= 0 || end > len(resources) {
		end = len(resources)
	}
	return resources[offset:end]
}

type Action struct {
	Name               string         `json:"name"`
	Version            int            `json:"version"`
	DisplayName        string         `json:"display_name"`
	Description        string         `json:"description"`
	ResourceType       string         `json:"resource_type"`
	RiskLevel          string         `json:"risk_level"`
	ExecutionMode      string         `json:"execution_mode"`
	ParameterSchema    map[string]any `json:"parameter_schema"`
	TimeoutSeconds     int            `json:"timeout_seconds"`
	Available          bool           `json:"available"`
	AvailabilityCode   string         `json:"availability_code"`
	AvailabilityReason string         `json:"availability_reason"`
	RequiredCapability string         `json:"required_capability"`
}

func (c *Client) ListResourceActions(ctx context.Context, resourceID string) ([]Action, error) {
	var actions []Action
	path := "/resources/" + url.PathEscape(resourceID) + "/actions"
	if err := c.get(ctx, path, nil, &actions); err != nil {
		return nil, err
	}
	if actions == nil {
		actions = []Action{}
	}
	return actions, nil
}

type Execution struct {
	ID                string         `json:"id"`
	Status            string         `json:"status"`
	ActionName        string         `json:"actionName"`
	ActionDisplayName string         `json:"actionDisplayName"`
	RiskLevel         string         `json:"riskLevel"`
	ResourceID        string         `json:"resourceId"`
	ResourceName      string         `json:"resourceName"`
	ResourceType      string         `json:"resourceType"`
	TraceID           string         `json:"traceId"`
	ErrorCode         string         `json:"errorCode"`
	ErrorMessage      string         `json:"errorMessage"`
	Result            map[string]any `json:"result"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}

func (c *Client) GetExecution(ctx context.Context, executionID string) (*Execution, error) {
	var execution Execution
	path := "/executions/" + url.PathEscape(executionID)
	if err := c.get(ctx, path, nil, &execution); err != nil {
		return nil, err
	}
	return &execution, nil
}

func (c *Client) WaitExecution(ctx context.Context, executionID string, timeout, poll time.Duration) (*Execution, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if poll <= 0 {
		poll = c.pollInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		execution, err := c.GetExecution(ctx, executionID)
		if err != nil {
			return nil, err
		}
		if IsTerminal(execution.Status) || !time.Now().Before(deadline) {
			return execution, nil
		}
		select {
		case <-ctx.Done():
			return execution, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func IsTerminal(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "CANCELLED", "EXPIRED", "UNKNOWN":
		return true
	default:
		return false
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	requestURL := c.baseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return &APIError{Code: "INVALID_ARGUMENT", Message: "请求参数无效"}
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Warden-Integration", "mcp")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &APIError{Code: "UNAVAILABLE", Message: "无法连接 Warden API"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return &APIError{Code: "UNAVAILABLE", Message: "读取 Warden API 响应失败"}
	}
	if len(body) > maxResponseBytes {
		return &APIError{Status: resp.StatusCode, Code: "RESPONSE_TOO_LARGE", Message: "Warden API 响应过大"}
	}
	if resp.StatusCode >= 400 {
		code, message := parseAPIErrorBody(resp.StatusCode, body)
		return &APIError{Status: resp.StatusCode, Code: code, Message: message}
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return &APIError{Code: "INVALID_RESPONSE", Message: "Warden API 返回了无法解析的响应"}
		}
	}
	return nil
}

func parseAPIErrorBody(status int, body []byte) (string, string) {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		code := strings.TrimSpace(payload.Error)
		message := strings.TrimSpace(payload.Message)
		if code != "" {
			if message == "" {
				message = defaultStatusMessage(status)
			}
			return code, message
		}
	}
	return statusErrorCode(status), defaultStatusMessage(status)
}

func statusErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	default:
		return "UNAVAILABLE"
	}
}

func defaultStatusMessage(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "Warden API 认证失败"
	case http.StatusForbidden:
		return "当前 Agent 没有该操作权限"
	case http.StatusNotFound:
		return "请求的资源不存在或不可见"
	case http.StatusTooManyRequests:
		return "请求过于频繁"
	default:
		return "Warden API 请求失败"
	}
}
