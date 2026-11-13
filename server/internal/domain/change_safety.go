package domain

const ChangeSafetyCapability = "pam.change-safety@v1"

type RollbackMode string

const (
	RollbackNone        RollbackMode = "NONE"
	RollbackManual      RollbackMode = "MANUAL"
	RollbackUnsupported RollbackMode = "NOT_SUPPORTED"
)

type ChangeSafetyContract struct {
	Change            bool         `json:"change"`
	SnapshotRequired  bool         `json:"snapshotRequired,omitempty"`
	VerificationMode  string       `json:"verificationMode,omitempty"`
	UnknownResolution string       `json:"unknownResolution,omitempty"`
	RollbackMode      RollbackMode `json:"rollbackMode"`
}

// ActionSafetyContract is the control-plane/Connector contract for mutations.
// Risk-low actions are observational and retain the existing lightweight path.
func ActionSafetyContract(actionName string, risk RiskLevel) ChangeSafetyContract {
	if risk == RiskLow {
		return ChangeSafetyContract{RollbackMode: RollbackNone}
	}
	contract := ChangeSafetyContract{
		Change: true, SnapshotRequired: true, VerificationMode: "RESOURCE_REREAD",
		UnknownResolution: "RESOURCE_REREAD_NO_RETRY", RollbackMode: RollbackUnsupported,
	}
	switch actionName {
	case "deployment.update_image", "deployment.scale", "statefulset.update_image", "statefulset.scale",
		"cronjob.suspend", "cronjob.resume", "jenkins.job.configure", "jenkins.job.enable", "jenkins.job.disable",
		"jenkins.node.set_offline", "grafana.folder.update", "grafana.dashboard.update", "grafana.datasource.update",
		"namespace.dashboard_configmap.apply", "systemd.service.start", "systemd.service.stop",
		"docker.container.start", "docker.container.stop", "huawei.ecs.start", "huawei.ecs.stop", "huawei.ecs.update_metadata",
		"kubernetes.resource.create", "kubernetes.resource.update", "kubernetes.resource.delete",
		"database.schema.create", "database.table.create",
		"network.interface.description.update", "network.interface.access_vlan.update",
		"network.interface.trunk_vlan.add", "network.interface.trunk_vlan.remove":
		contract.RollbackMode = RollbackManual
	}
	return contract
}

func SupportsChangeSafety(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == ChangeSafetyCapability {
			return true
		}
	}
	return false
}

func IsDeleteAction(actionName string) bool {
	return len(actionName) > len(".delete") && actionName[len(actionName)-len(".delete"):] == ".delete"
}

func IsCreateAction(actionName string) bool {
	return len(actionName) > len(".create") && actionName[len(actionName)-len(".create"):] == ".create"
}
