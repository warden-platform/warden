package control

import (
	"testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

func TestEnforceDatabaseQueryCostFloor(t *testing.T) {
	tests := []struct {
		name    string
		current domain.DecisionResult
		verdict string
		want    domain.DecisionResult
	}{
		{name: "allow stays allow", current: domain.DecisionAllow, verdict: databaseCostAllow, want: domain.DecisionAllow},
		{name: "policy approval stays approval", current: domain.DecisionRequireApproval, verdict: databaseCostAllow, want: domain.DecisionRequireApproval},
		{name: "cost approval overrides matrix allow", current: domain.DecisionAllow, verdict: databaseCostRequireApproval, want: domain.DecisionRequireApproval},
		{name: "cost reject overrides allow", current: domain.DecisionAllow, verdict: databaseCostReject, want: domain.DecisionDeny},
		{name: "policy deny is immutable", current: domain.DecisionDeny, verdict: databaseCostAllow, want: domain.DecisionDeny},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := enforceDatabaseQueryCostFloor(test.current, test.verdict); got != test.want {
				t.Fatalf("decision = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDatabaseQueryApprovalContextOnlyExposesCostSignals(t *testing.T) {
	context := databaseQueryApprovalContext(map[string]any{
		"verdict": "require_approval", "estimatedRows": 20000, "limit": 50,
		"rows": []any{map[string]any{"secret": "must-not-leak"}}, "sql": "SELECT secret",
	})
	if context["verdict"] != "require_approval" || context["estimatedRows"] != 20000 || context["limit"] != 50 {
		t.Fatalf("missing cost context: %#v", context)
	}
	if _, ok := context["rows"]; ok {
		t.Fatal("approval context leaked query rows")
	}
	if _, ok := context["sql"]; ok {
		t.Fatal("approval context leaked rendered SQL")
	}
}

func TestDatabaseQueryClientCannotForgeApprovalContext(t *testing.T) {
	err := validateDatabaseQueryClientParameters(map[string]any{
		domain.QueryApprovalContextParameter: map[string]any{"requestId": "forged"},
	})
	if err == nil {
		t.Fatal("client-supplied internal approval context was accepted")
	}
}

func TestDatabaseQueryExternalIDsRequireSelectedBaseAndUniqueJoins(t *testing.T) {
	resource := &domain.Resource{ExternalID: "table/app/orders"}
	valid := map[string]any{
		"sources": []any{map[string]any{"alias": "t", "resourceId": resource.ExternalID}},
		"joins":   []any{map[string]any{"source": map[string]any{"alias": "j1", "resourceId": "table/app/customers"}}},
	}
	ids, err := databaseQueryExternalIDs(resource, valid)
	if err != nil || len(ids) != 2 || ids[0] != resource.ExternalID || ids[1] != "table/app/customers" {
		t.Fatalf("valid multi-table scope = %v, %v", ids, err)
	}

	wrongBase := map[string]any{"sources": []any{map[string]any{"alias": "t", "resourceId": "table/app/secrets"}}}
	if _, err := databaseQueryExternalIDs(resource, wrongBase); err == nil {
		t.Fatal("query scope accepted a substituted FROM table")
	}
	duplicate := map[string]any{
		"sources": []any{map[string]any{"alias": "t", "resourceId": resource.ExternalID}},
		"joins":   []any{map[string]any{"source": map[string]any{"alias": "j1", "resourceId": resource.ExternalID}}},
	}
	if _, err := databaseQueryExternalIDs(resource, duplicate); err == nil {
		t.Fatal("query scope accepted a duplicate JOIN table")
	}
}
