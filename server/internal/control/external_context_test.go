package control

import (
	"testing"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

func TestNormalizeExternalExecutionContext(t *testing.T) {
	got, err := normalizeExternalExecutionContext(&domain.ExternalExecutionContext{
		Source: "federated", WorkspaceID: "workspace-1", ProjectID: "external-project-1",
		AgentID: "agent-1", TaskID: "task-1", OriginatorUserID: "user-1",
		AccountableUserID: "user-1", OriginatorSource: "human",
	})
	if err != nil {
		t.Fatalf("normalize valid context: %v", err)
	}
	if got["source"] != "federated" || got["workspaceId"] != "workspace-1" || got["taskId"] != "task-1" {
		t.Fatalf("unexpected normalized context: %#v", got)
	}
	if _, err := normalizeExternalExecutionContext(&domain.ExternalExecutionContext{Source: "Federated", TaskID: "task-1"}); err == nil {
		t.Fatal("uppercase source was accepted")
	}
	if _, err := normalizeExternalExecutionContext(&domain.ExternalExecutionContext{Source: "federated"}); err == nil {
		t.Fatal("context without correlation id was accepted")
	}
	if _, err := normalizeExternalExecutionContext(&domain.ExternalExecutionContext{Source: "federated", TaskID: "task with spaces"}); err == nil {
		t.Fatal("unsafe correlation id was accepted")
	}
}

func TestExternalContextsEqual(t *testing.T) {
	left := map[string]any{"source": "federated", "taskId": "task-1"}
	right := map[string]any{"taskId": "task-1", "source": "federated"}
	if !externalContextsEqual(left, right) {
		t.Fatal("equivalent contexts were not equal")
	}
	if externalContextsEqual(left, map[string]any{"source": "federated", "taskId": "task-2"}) {
		t.Fatal("different contexts were equal")
	}
}
