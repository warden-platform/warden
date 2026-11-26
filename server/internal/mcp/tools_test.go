package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListResourcesFiltersAndOmitsMetadata(t *testing.T) {
	client, capabilities := newFakeWarden(t, []string{scopeResourcesRead, scopeExecutionRead})
	tools := ReadOnlyTools(client, capabilities, 5*time.Second)
	tool := findToolByName(t, tools, "warden_list_resources")

	result, err := tool.Handler(context.Background(), map[string]any{"query": "alpha", "limit": 10})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	payload := toolPayload(t, result)
	rawJSON := mustJSON(t, result)
	if strings.Contains(rawJSON, "top-secret") || strings.Contains(rawJSON, "metadata") {
		t.Fatalf("resource metadata leaked: %s", rawJSON)
	}

	resources := payload["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1: %s", len(resources), rawJSON)
	}
	resource := resources[0].(map[string]any)
	if resource["name"] != "alpha-deployment" {
		t.Errorf("resource name = %v", resource["name"])
	}
	if resource["available_action_count"] != float64(3) {
		t.Errorf("available_action_count = %v", resource["available_action_count"])
	}
}

func TestListResourceActionsReturnsSchema(t *testing.T) {
	client, capabilities := newFakeWarden(t, []string{scopeResourcesRead, scopeExecutionRead})
	tools := ReadOnlyTools(client, capabilities, 5*time.Second)
	tool := findToolByName(t, tools, "warden_list_resource_actions")

	result, err := tool.Handler(context.Background(), map[string]any{"resource_id": "res-1"})
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	payload := toolPayload(t, result)
	actions := payload["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	action := actions[0].(map[string]any)
	if action["name"] != "deployment.get" || action["risk_level"] != "low" {
		t.Errorf("unexpected action: %v", action)
	}
	if _, ok := action["parameter_schema"].(map[string]any); !ok {
		t.Errorf("parameter_schema missing: %v", action)
	}
}

func TestGetExecutionAndWaitExecution(t *testing.T) {
	client, capabilities := newFakeWarden(t, []string{scopeResourcesRead, scopeExecutionRead})
	tools := ReadOnlyTools(client, capabilities, 5*time.Second)

	get := findToolByName(t, tools, "warden_get_execution")
	result, err := get.Handler(context.Background(), map[string]any{"execution_id": "exec-1"})
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	payload := toolPayload(t, result)
	if payload["status"] != "WAITING_APPROVAL" || payload["terminal"] != false {
		t.Errorf("unexpected execution summary: %v", payload)
	}

	wait := findToolByName(t, tools, "warden_wait_execution")
	result, err = wait.Handler(context.Background(), map[string]any{"execution_id": "wait-1", "timeout_seconds": 2})
	if err != nil {
		t.Fatalf("wait execution: %v", err)
	}
	payload = toolPayload(t, result)
	if payload["status"] != "SUCCEEDED" || payload["terminal"] != true {
		t.Errorf("unexpected wait summary: %v", payload)
	}
}

func TestCapabilityFilteringAndValidation(t *testing.T) {
	client, _ := newFakeWarden(t, []string{scopeResourcesRead})
	resourceTools := ReadOnlyTools(client, []string{scopeResourcesRead}, 5*time.Second)
	if len(resourceTools) != 2 {
		t.Fatalf("resource-only capabilities exposed %d tools, want 2", len(resourceTools))
	}
	for _, tool := range resourceTools {
		if strings.Contains(tool.Name, "execution") {
			t.Errorf("execution tool exposed without execution scope: %s", tool.Name)
		}
	}

	executionTools := ReadOnlyTools(client, []string{scopeExecutionRead}, 5*time.Second)
	if len(executionTools) != 2 {
		t.Fatalf("execution-only capabilities exposed %d tools, want 2", len(executionTools))
	}
	for _, tool := range executionTools {
		if strings.Contains(tool.Name, "resource") {
			t.Errorf("resource tool exposed without resource scope: %s", tool.Name)
		}
	}

	list := findToolByName(t, resourceTools, "warden_list_resources")
	if _, err := list.Handler(context.Background(), map[string]any{"limit": 100}); err == nil {
		t.Fatal("expected limit validation error")
	} else {
		var toolErr *ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_ARGUMENT" {
			t.Fatalf("unexpected limit error: %v", err)
		}
	}
}

func TestAPIErrorIsMappedToToolError(t *testing.T) {
	client, capabilities := newFakeWarden(t, []string{scopeResourcesRead})
	tools := ReadOnlyTools(client, capabilities, 5*time.Second)
	tool := findToolByName(t, tools, "warden_list_resource_actions")

	_, err := tool.Handler(context.Background(), map[string]any{"resource_id": "forbidden"})
	if err == nil {
		t.Fatal("expected forbidden error")
	}
	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error is not ToolError: %v", err)
	}
	if toolErr.Code != "FORBIDDEN" || toolErr.Message != "no access" {
		t.Fatalf("unexpected tool error: %+v", toolErr)
	}
}

func newFakeWarden(t *testing.T, capabilities []string) (*Client, []string) {
	t.Helper()
	var waitCalls int32

	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-key" {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "UNAUTHORIZED", "message": "bad key"})
				return
			}
			next(w, r)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/me", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"name": "agent", "capabilities": capabilities})
	}))
	mux.HandleFunc("/resources", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]any{
			{
				"id": "res-1", "name": "alpha-deployment", "resource_type": "kubernetes.deployment",
				"external_id": "alpha", "namespace": "prod", "project_id": "proj-1", "project_name": "Payments",
				"environment": "production", "status": "ACTIVE", "connector_id": "conn-1",
				"available_action_count": 3,
				"metadata":               map[string]any{"secret": "top-secret"},
				"labels":                 map[string]any{"secret": "top-secret"},
			},
			{
				"id": "res-2", "name": "beta-service", "resource_type": "kubernetes.service",
				"environment": "testing", "status": "ACTIVE", "available_action_count": 1,
			},
		})
	}))
	mux.HandleFunc("/resources/res-1/actions", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]any{{
			"name": "deployment.get", "version": 1, "display_name": "Get deployment",
			"description": "Read a deployment", "resource_type": "kubernetes.deployment",
			"risk_level": "low", "execution_mode": "sync", "timeout_seconds": 30,
			"parameter_schema": map[string]any{"type": "object"},
			"available":        true, "required_capability": "deployment.get@v1",
		}})
	}))
	mux.HandleFunc("/resources/forbidden/actions", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "FORBIDDEN", "message": "no access"})
	}))
	mux.HandleFunc("/executions/exec-1", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "exec-1", "status": "WAITING_APPROVAL", "actionName": "deployment.get",
			"resourceId": "res-1", "resourceName": "alpha-deployment", "resourceType": "kubernetes.deployment",
			"traceId": "trace-1", "createdAt": "2026-11-26T00:00:00Z", "updatedAt": "2026-11-26T00:00:00Z",
		})
	}))
	mux.HandleFunc("/executions/wait-1", auth(func(w http.ResponseWriter, r *http.Request) {
		status := "QUEUED"
		if atomic.AddInt32(&waitCalls, 1) >= 2 {
			status = "SUCCEEDED"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "wait-1", "status": status, "actionName": "deployment.get",
			"resourceId": "res-1", "traceId": "trace-2",
			"createdAt": "2026-11-26T00:00:00Z", "updatedAt": "2026-11-26T00:00:00Z",
		})
	}))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL, "test-key", 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.SetPollInterval(10 * time.Millisecond)
	return client, capabilities
}

func findToolByName(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %s not found", name)
	return Tool{}
}

func toolPayload(t *testing.T, value any) map[string]any {
	t.Helper()
	raw := mustJSON(t, value)
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode tool payload: %v", err)
	}
	return payload
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(payload)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
