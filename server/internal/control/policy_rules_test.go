package control

import (
	"testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

func TestCombinePolicyDecisionPreservesRegoDeny(t *testing.T) {
	tests := []struct {
		base domain.DecisionResult
		rule domain.DecisionResult
		want domain.DecisionResult
	}{
		{domain.DecisionDeny, domain.DecisionAllow, domain.DecisionDeny},
		{domain.DecisionRequireApproval, domain.DecisionAllow, domain.DecisionAllow},
		{domain.DecisionAllow, domain.DecisionRequireApproval, domain.DecisionRequireApproval},
		{domain.DecisionAllow, domain.DecisionDeny, domain.DecisionDeny},
	}
	for _, test := range tests {
		got := combinePolicyDecision(test.base, &domain.PolicyMatrixRule{Decision: test.rule})
		if got != test.want {
			t.Fatalf("base=%s rule=%s got=%s want=%s", test.base, test.rule, got, test.want)
		}
	}
}

func TestEnrichPolicyRuleConflictsAndShadowing(t *testing.T) {
	rules := []domain.PolicyMatrixRule{
		{ID: "broad", Enabled: true, Priority: 200, ResourceType: "*", ActionName: "*", Environment: "*"},
		{ID: "specific", Enabled: true, Priority: 100, ResourceType: "huawei_ecs_instance", ActionName: "huawei.ecs.get", Environment: "production"},
		{ID: "conflict-a", Enabled: true, Priority: 50, ResourceType: "database_schema", ActionName: "*", Environment: "production"},
		{ID: "conflict-b", Enabled: true, Priority: 50, ResourceType: "database_schema", ActionName: "database.schema.get", Environment: "*"},
	}
	enrichPolicyRuleConflicts(rules)
	if rules[1].ShadowedBy != "broad" {
		t.Fatalf("specific rule should be shadowed by broad rule: %#v", rules[1])
	}
	if len(rules[2].ConflictIDs) != 1 || rules[2].ConflictIDs[0] != "conflict-b" {
		t.Fatalf("overlapping same-priority rules should conflict: %#v", rules[2])
	}
}
