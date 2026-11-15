package control

import (
	"strings"
	"testing"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func TestConnectorExecutionErrorKeepsKnownJenkinsCodes(t *testing.T) {
	code, message := connectorExecutionError("JENKINS_JOB_CONFLICT: Jenkins Job already exists")
	if code != "JENKINS_JOB_CONFLICT" || !strings.Contains(message, "already exists") {
		t.Fatalf("unexpected mapped error: %s %q", code, message)
	}
	code, message = connectorExecutionError("RESOURCE_VERSION_CONFLICT: expected 10, current 11")
	if code != "RESOURCE_VERSION_CONFLICT" || !strings.Contains(message, "current 11") {
		t.Fatalf("unexpected Kubernetes conflict mapping: %s %q", code, message)
	}
	code, _ = connectorExecutionError("SOME_UNTRUSTED_CODE: failure")
	if code != "EXECUTION_ERROR" {
		t.Fatalf("untrusted connector error code was accepted: %s", code)
	}
}

func TestProductionChangeApprovalFloor(t *testing.T) {
	change := domain.ActionSafetyContract("deployment.update_image", domain.RiskHigh)
	read := domain.ActionSafetyContract("deployment.get", domain.RiskLow)
	if got := enforceProductionChangeApproval(domain.DecisionAllow, "production", "deployment.update_image", change); got != domain.DecisionRequireApproval {
		t.Fatalf("production change = %s", got)
	}
	if got := enforceProductionChangeApproval(domain.DecisionAllow, "testing", "deployment.update_image", change); got != domain.DecisionAllow {
		t.Fatalf("testing change = %s", got)
	}
	if got := enforceProductionChangeApproval(domain.DecisionAllow, "production", "deployment.get", read); got != domain.DecisionAllow {
		t.Fatalf("production read = %s", got)
	}
	folderUpdate := domain.ActionSafetyContract("grafana.folder.update", domain.RiskHigh)
	if got := enforceProductionChangeApproval(domain.DecisionAllow, "production", "grafana.folder.update", folderUpdate); got != domain.DecisionRequireApproval {
		t.Fatalf("production Grafana folder update = %s", got)
	}
	jenkinsCreate := domain.ActionSafetyContract("jenkins.job.create", domain.RiskHigh)
	if got := enforceProductionChangeApproval(domain.DecisionAllow, "production", "jenkins.job.create", jenkinsCreate); got != domain.DecisionAllow {
		t.Fatalf("production Jenkins Job create = %s, want direct execution", got)
	}
}

func TestJenkinsJobCreateApprovalExemption(t *testing.T) {
	if got := enforceActionApprovalExemption(domain.DecisionRequireApproval, "jenkins.job.create"); got != domain.DecisionAllow {
		t.Fatalf("Jenkins Job create = %s, want direct execution", got)
	}
	if got := enforceActionApprovalExemption(domain.DecisionDeny, "jenkins.job.create"); got != domain.DecisionDeny {
		t.Fatalf("Jenkins Job create explicit deny = %s, want deny", got)
	}
	if got := enforceActionApprovalExemption(domain.DecisionRequireApproval, "jenkins.job.update"); got != domain.DecisionRequireApproval {
		t.Fatalf("Jenkins Job update = %s, want approval", got)
	}
}

func TestExecutionResultAuditMetadataKeepsOnlySnapshotSummary(t *testing.T) {
	result := map[string]any{"_pam_safety": map[string]any{
		"contractVersion": float64(1), "verification": "VERIFIED", "rollbackMode": "MANUAL",
		"before": map[string]any{"present": true, "name": "api", "stateHash": "old-hash", "state": map[string]any{"token": "must-not-enter-audit"}},
		"after":  map[string]any{"present": true, "name": "api", "stateHash": "new-hash", "state": map[string]any{"token": "must-not-enter-audit"}},
	}}
	metadata := executionResultAuditMetadata(result)
	before := metadata["before_summary"].(map[string]any)
	after := metadata["after_summary"].(map[string]any)
	if before["stateHash"] != "old-hash" || after["stateHash"] != "new-hash" {
		t.Fatalf("snapshot hashes missing: %#v", metadata)
	}
	if _, leaked := before["state"]; leaked {
		t.Fatalf("raw state leaked into audit: %#v", before)
	}
}

func TestDuplicateTerminalResultAcceptsSameStatusAndRejectsConflict(t *testing.T) {
	current := &domain.Execution{Status: domain.ExecSucceeded}
	if !acceptsDuplicateTerminalResult(current, domain.ExecSucceeded) {
		t.Fatal("the same terminal result should be accepted idempotently")
	}
	if acceptsDuplicateTerminalResult(current, domain.ExecFailed) {
		t.Fatal("a conflicting terminal result must be rejected")
	}
	if acceptsDuplicateTerminalResult(nil, domain.ExecSucceeded) {
		t.Fatal("a missing execution must not be treated as an idempotent result")
	}
}
