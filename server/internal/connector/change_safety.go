package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

const safetyResultKey = "_pam_safety"

var errHuaweiCreatePartial = errors.New("Huawei ECS creation partially completed")

type snapshotRunner interface {
	SnapshotResource(context.Context, domain.TaskResource) (DiscoveredResource, bool)
}

func (r *Runtime) executeTask(ctx context.Context, task domain.TaskEnvelope) (string, map[string]any, Lease, string) {
	if task.Safety == nil || !task.Safety.Change {
		result, lease, err := r.runner.Execute(ctx, task)
		if err == nil {
			return "SUCCEEDED", result, lease, ""
		}
		var approvalRequired *queryApprovalRequiredError
		if errors.As(err, &approvalRequired) {
			return "FAILED", approvalRequired.result, lease, err.Error()
		}
		status := "FAILED"
		if IsUnknownOutcome(task.Action.Name, err) {
			status = "UNKNOWN"
		}
		return status, nil, lease, err.Error()
	}

	before, found := r.snapshotTaskResource(ctx, task.Resource)
	if !found {
		return "FAILED", map[string]any{safetyResultKey: safetyMetadata(task, nil, nil, "PRECONDITION_FAILED")}, Lease{},
			"STATE_SNAPSHOT_FAILED: 变更前无法读取目标资源，已阻止执行"
	}

	operation, lease, err := r.runner.Execute(ctx, task)
	if err != nil {
		if !IsUnknownOutcome(task.Action.Name, err) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return "FAILED", attachSafety(operation, safetyMetadata(task, before, nil, "EXECUTION_FAILED")), lease, err.Error()
		}
		reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		observed, _ := r.snapshotTaskResource(reconcileCtx, task.Resource)
		cancel()
		metadata := safetyMetadata(task, before, observed, "RECONCILIATION_REQUIRED")
		metadata["unknownResolution"] = "目标状态已重新读取；禁止自动重试，需要人工或 Agent 根据观测结果裁决"
		return "UNKNOWN", attachSafety(operation, metadata), lease, err.Error()
	}

	verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	after, afterFound := r.snapshotTaskResource(verifyCtx, task.Resource)
	cancel()
	verification := "VERIFIED"
	if domain.IsDeleteAction(task.Action.Name) && !afterFound {
		verification = "VERIFIED_ABSENT"
	} else if domain.IsDeleteAction(task.Action.Name) && operation["deletionSubmitted"] == true {
		verification = "ACCEPTED_ASYNC"
	} else if domain.IsCreateAction(task.Action.Name) && afterFound {
		verification = "VERIFIED_BY_RESULT_AND_PARENT_REREAD"
	} else if !afterFound {
		metadata := safetyMetadata(task, before, nil, "RECONCILIATION_REQUIRED")
		metadata["unknownResolution"] = "变更已返回成功，但无法重新读取目标状态；禁止自动重试"
		return "UNKNOWN", attachSafety(operation, metadata), lease, "POST_CHANGE_VERIFICATION_FAILED: 目标状态不可读取"
	}
	return "SUCCEEDED", attachSafety(operation, safetyMetadata(task, before, after, verification)), lease, ""
}

func (r *Runtime) snapshotTaskResource(ctx context.Context, resource domain.TaskResource) (map[string]any, bool) {
	var discovered DiscoveredResource
	var found bool
	if runner, ok := r.runner.(snapshotRunner); ok {
		discovered, found = runner.SnapshotResource(ctx, resource)
	} else {
		discovered, found = findDiscoveredResource(r.runner.ListResources(ctx), resource)
	}
	if !found {
		return map[string]any{"present": false, "observedAt": time.Now().UTC()}, false
	}
	state := map[string]any{"labels": discovered.Labels, "annotations": discovered.Annotations, "metadata": discovered.Metadata}
	return map[string]any{
		"present": true, "externalId": discovered.ExternalID, "name": discovered.Name,
		"namespace": discovered.Namespace, "resourceType": discovered.ResourceType,
		"integrationId": discovered.IntegrationID, "stateHash": domain.HashParameters(state),
		"state": state, "observedAt": time.Now().UTC(),
	}, true
}

func findDiscoveredResource(resources []DiscoveredResource, target domain.TaskResource) (DiscoveredResource, bool) {
	for _, resource := range resources {
		if resource.ResourceType != target.Type || resource.IntegrationID != target.IntegrationID {
			continue
		}
		if target.ExternalID != "" && resource.ExternalID == target.ExternalID {
			return resource, true
		}
		if target.ExternalID == "" && resource.Name == target.Name && resource.Namespace == target.Namespace {
			return resource, true
		}
	}
	return DiscoveredResource{}, false
}

func safetyMetadata(task domain.TaskEnvelope, before, after map[string]any, verification string) map[string]any {
	metadata := map[string]any{
		"contractVersion": 1, "verification": verification,
		"rollbackMode": task.Safety.RollbackMode, "unknownResolutionMode": task.Safety.UnknownResolution,
	}
	if before != nil {
		metadata["before"] = before
	}
	if after != nil {
		metadata["after"] = after
	}
	return metadata
}

func attachSafety(result map[string]any, metadata map[string]any) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	if _, exists := result[safetyResultKey]; exists {
		result["operationSafetyCollision"] = fmt.Sprint(result[safetyResultKey])
	}
	result[safetyResultKey] = metadata
	return result
}
