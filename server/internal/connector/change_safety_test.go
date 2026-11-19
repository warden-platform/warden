package connector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

type safetyTestRunner struct {
	snapshots [][]DiscoveredResource
	executed  int
	result    map[string]any
	err       error
}

func (r *safetyTestRunner) ListResources(context.Context) []DiscoveredResource {
	if len(r.snapshots) == 0 {
		return nil
	}
	result := r.snapshots[0]
	if len(r.snapshots) > 1 {
		r.snapshots = r.snapshots[1:]
	}
	return result
}

func (r *safetyTestRunner) Execute(context.Context, domain.TaskEnvelope) (map[string]any, Lease, error) {
	r.executed++
	return r.result, Lease{Provider: "test"}, r.err
}

func safetyTask() domain.TaskEnvelope {
	contract := domain.ActionSafetyContract("deployment.update_image", domain.RiskHigh)
	return domain.TaskEnvelope{
		Resource: domain.TaskResource{ExternalID: "deployment/default/api", Type: "deployment", Name: "api", Namespace: "default"},
		Action:   domain.TaskAction{Name: "deployment.update_image", Version: 1}, Safety: &contract,
		Grant: domain.TaskGrant{ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func discoveredDeployment(image string) DiscoveredResource {
	return DiscoveredResource{ExternalID: "deployment/default/api", ResourceType: "deployment", Name: "api", Namespace: "default", Metadata: map[string]any{"image": image}}
}

func TestExecuteTaskCapturesAndVerifiesChange(t *testing.T) {
	runner := &safetyTestRunner{snapshots: [][]DiscoveredResource{{discoveredDeployment("old")}, {discoveredDeployment("new")}}, result: map[string]any{"image": "new"}}
	runtime := &Runtime{runner: runner}
	status, result, _, message := runtime.executeTask(context.Background(), safetyTask())
	if status != "SUCCEEDED" || message != "" || runner.executed != 1 {
		t.Fatalf("executeTask = (%s, %#v, %q), executions=%d", status, result, message, runner.executed)
	}
	safety, ok := result[safetyResultKey].(map[string]any)
	if !ok || safety["verification"] != "VERIFIED" || safety["before"] == nil || safety["after"] == nil {
		t.Fatalf("missing safety result: %#v", result)
	}
}

func TestExecuteTaskUnknownRereadsWithoutRetry(t *testing.T) {
	runner := &safetyTestRunner{snapshots: [][]DiscoveredResource{{discoveredDeployment("old")}, {discoveredDeployment("new")}}, err: context.DeadlineExceeded}
	runtime := &Runtime{runner: runner}
	status, result, _, _ := runtime.executeTask(context.Background(), safetyTask())
	if status != "UNKNOWN" || runner.executed != 1 {
		t.Fatalf("status=%s executions=%d", status, runner.executed)
	}
	safety := result[safetyResultKey].(map[string]any)
	if safety["verification"] != "RECONCILIATION_REQUIRED" || safety["after"] == nil || safety["unknownResolution"] == nil {
		t.Fatalf("unexpected UNKNOWN safety result: %#v", safety)
	}
}

func TestExecuteTaskBlocksMutationWhenSnapshotFails(t *testing.T) {
	runner := &safetyTestRunner{snapshots: [][]DiscoveredResource{{}}, err: errors.New("must not execute")}
	runtime := &Runtime{runner: runner}
	status, _, _, message := runtime.executeTask(context.Background(), safetyTask())
	if status != "FAILED" || runner.executed != 0 || message == "" {
		t.Fatalf("status=%s executions=%d message=%q", status, runner.executed, message)
	}
}

func TestExecuteTaskPreservesRuntimeQueryApprovalAssessment(t *testing.T) {
	assessment := map[string]any{"verdict": string(CostRequireApproval), "estimatedRows": 25000.0}
	runner := &safetyTestRunner{err: &queryApprovalRequiredError{result: assessment}}
	runtime := &Runtime{runner: runner}
	task := domain.TaskEnvelope{Action: domain.TaskAction{Name: "database.query.execute", Version: 1}}
	status, result, _, message := runtime.executeTask(context.Background(), task)
	if status != "FAILED" || !strings.HasPrefix(message, "DATABASE_APPROVAL_REQUIRED:") || result["verdict"] != string(CostRequireApproval) {
		t.Fatalf("runtime escalation = (%s, %#v, %q)", status, result, message)
	}
}

func TestExecuteTaskMarksAcceptedAsyncDeleteHonestly(t *testing.T) {
	resource := discoveredDeployment("old")
	runner := &safetyTestRunner{
		snapshots: [][]DiscoveredResource{{resource}, {resource}},
		result:    map[string]any{"deletionSubmitted": true, "accepted": map[string]any{"job_id": "delete-job"}},
	}
	runtime := &Runtime{runner: runner}
	task := safetyTask()
	task.Action.Name = "huawei.ecs.delete"
	contract := domain.ActionSafetyContract(task.Action.Name, domain.RiskHigh)
	task.Safety = &contract
	status, result, _, message := runtime.executeTask(context.Background(), task)
	if status != "SUCCEEDED" || message != "" {
		t.Fatalf("executeTask = (%s, %#v, %q)", status, result, message)
	}
	safety := result[safetyResultKey].(map[string]any)
	if safety["verification"] != "ACCEPTED_ASYNC" || safety["after"] == nil {
		t.Fatalf("unexpected async delete safety result: %#v", safety)
	}
}
