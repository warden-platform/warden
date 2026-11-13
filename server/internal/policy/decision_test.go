package policy

import (
	"context"
	"strings"
	"testing"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

func TestBuiltinProviderDecisions(t *testing.T) {
	provider, err := NewEmbeddedProvider(embeddedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		environment string
		action      string
		status      string
		want        domain.DecisionResult
	}{
		{"testing write", "testing", "deployment.update_image", "ACTIVE", domain.DecisionAllow},
		{"testing deployment restart", "testing", "deployment.restart", "ACTIVE", domain.DecisionAllow},
		{"testing statefulset scale", "testing", "statefulset.scale", "ACTIVE", domain.DecisionAllow},
		{"testing cronjob trigger", "testing", "cronjob.trigger", "ACTIVE", domain.DecisionAllow},
		{"development write", "development", "deployment.update_image", "ACTIVE", domain.DecisionAllow},
		{"production read", "production", "deployment.get", "ACTIVE", domain.DecisionAllow},
		{"production write", "production", "deployment.update_image", "ACTIVE", domain.DecisionRequireApproval},
		{"production deployment scale", "production", "deployment.scale", "ACTIVE", domain.DecisionRequireApproval},
		{"production statefulset image", "production", "statefulset.update_image", "ACTIVE", domain.DecisionRequireApproval},
		{"production cronjob suspend", "production", "cronjob.suspend", "ACTIVE", domain.DecisionRequireApproval},
		{"testing generic Kubernetes create", "testing", "kubernetes.resource.create", "ACTIVE", domain.DecisionAllow},
		{"production generic Kubernetes read", "production", "kubernetes.resource.get", "ACTIVE", domain.DecisionAllow},
		{"production generic Kubernetes preview", "production", "kubernetes.resource.preview", "ACTIVE", domain.DecisionAllow},
		{"production generic Kubernetes update", "production", "kubernetes.resource.update", "ACTIVE", domain.DecisionRequireApproval},
		{"production generic Kubernetes delete", "production", "kubernetes.resource.delete", "ACTIVE", domain.DecisionRequireApproval},
		{"governed provider read", "production", "provider.request", "ACTIVE", domain.DecisionAllow},
		{"disabled principal", "testing", "deployment.get", "DISABLED", domain.DecisionDeny},
		{"unknown action", "testing", "deployment.raw_patch", "ACTIVE", domain.DecisionDeny},
		{"linux read", "testing", "linux.host.info", "ACTIVE", domain.DecisionAllow},
		{"testing systemd restart", "testing", "systemd.service.restart", "ACTIVE", domain.DecisionAllow},
		{"testing Docker stop", "testing", "docker.container.stop", "ACTIVE", domain.DecisionAllow},
		{"production systemd start", "production", "systemd.service.start", "ACTIVE", domain.DecisionRequireApproval},
		{"production Docker restart", "production", "docker.container.restart", "ACTIVE", domain.DecisionRequireApproval},
		{"jenkins read", "production", "jenkins.job.get", "ACTIVE", domain.DecisionAllow},
		{"jenkins create", "testing", "jenkins.job.create", "ACTIVE", domain.DecisionAllow},
		{"jenkins create in production", "production", "jenkins.job.create", "ACTIVE", domain.DecisionAllow},
		{"jenkins change", "testing", "jenkins.job.build", "ACTIVE", domain.DecisionRequireApproval},
		{"jenkins update", "testing", "jenkins.job.update", "ACTIVE", domain.DecisionRequireApproval},
		{"jenkins delete", "testing", "jenkins.job.delete", "ACTIVE", domain.DecisionRequireApproval},
		{"database schema read", "production", "database.schema.get", "ACTIVE", domain.DecisionAllow},
		{"database bounded rows read", "production", "database.table.rows.query", "ACTIVE", domain.DecisionAllow},
		{"database query preview", "production", "database.query.preview", "ACTIVE", domain.DecisionAllow},
		{"database query execute", "production", "database.query.execute", "ACTIVE", domain.DecisionAllow},
		{"database query template execute", "production", "database.query_template.execute", "ACTIVE", domain.DecisionAllow},
		{"database table create preview", "production", "database.table.create.preview", "ACTIVE", domain.DecisionAllow},
		{"database table create", "testing", "database.table.create", "ACTIVE", domain.DecisionRequireApproval},
		{"Huawei ECS read", "production", "huawei.ecs.get", "ACTIVE", domain.DecisionAllow},
		{"Huawei ECS create", "production", "huawei.ecs.create", "ACTIVE", domain.DecisionRequireApproval},
		{"Huawei ECS start", "testing", "huawei.ecs.start", "ACTIVE", domain.DecisionRequireApproval},
		{"Huawei ECS stop", "production", "huawei.ecs.stop", "ACTIVE", domain.DecisionRequireApproval},
		{"Huawei ECS reboot", "production", "huawei.ecs.reboot", "ACTIVE", domain.DecisionRequireApproval},
		{"Huawei ECS metadata", "testing", "huawei.ecs.update_metadata", "ACTIVE", domain.DecisionRequireApproval},
		{"Huawei ECS delete", "testing", "huawei.ecs.delete", "ACTIVE", domain.DecisionRequireApproval},
		{"network device read", "production", "network.device.audit", "ACTIVE", domain.DecisionAllow},
		{"network device health read", "production", "network.device.health", "ACTIVE", domain.DecisionAllow},
		{"network interface preview", "production", "network.interface.access_vlan.preview", "ACTIVE", domain.DecisionAllow},
		{"network interface production change", "production", "network.interface.access_vlan.update", "ACTIVE", domain.DecisionRequireApproval},
		{"network interface testing change", "testing", "network.interface.description.update", "ACTIVE", domain.DecisionRequireApproval},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := provider.Evaluate(context.Background(), Input{
				Principal: PrincipalInfo{Status: test.status}, Environment: test.environment, Action: test.action,
				Connector: ConnectorInfo{Type: func() string {
					if strings.HasPrefix(test.action, "network.") {
						return "network"
					}
					return map[bool]string{true: "linux", false: "kubernetes"}[test.action == "linux.host.info" || strings.HasPrefix(test.action, "systemd.") || strings.HasPrefix(test.action, "docker.")]
				}()},
			})
			if err != nil {
				t.Fatal(err)
			}
			if output.Decision != test.want {
				t.Fatalf("got %s, want %s", output.Decision, test.want)
			}
			if output.PolicyBundleHash == "" || len(output.ReasonCodes) == 0 {
				t.Fatal("decision is missing audit metadata")
			}
		})
	}
}
