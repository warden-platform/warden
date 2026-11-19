package domain

import "testing"

func TestActionSafetyContract(t *testing.T) {
	read := ActionSafetyContract("deployment.get", RiskLow)
	if read.Change || read.RollbackMode != RollbackNone {
		t.Fatalf("read contract = %#v", read)
	}
	update := ActionSafetyContract("deployment.update_image", RiskHigh)
	if !update.Change || !update.SnapshotRequired || update.RollbackMode != RollbackManual || update.UnknownResolution != "RESOURCE_REREAD_NO_RETRY" {
		t.Fatalf("update contract = %#v", update)
	}
	for _, action := range []string{"deployment.scale", "statefulset.scale", "statefulset.update_image", "cronjob.suspend", "cronjob.resume",
		"systemd.service.start", "systemd.service.stop", "docker.container.start", "docker.container.stop"} {
		contract := ActionSafetyContract(action, RiskHigh)
		if !contract.Change || contract.RollbackMode != RollbackManual {
			t.Fatalf("%s should expose manual rollback metadata: %#v", action, contract)
		}
	}
	for _, action := range []string{"huawei.ecs.start", "huawei.ecs.stop", "huawei.ecs.update_metadata"} {
		contract := ActionSafetyContract(action, RiskHigh)
		if !contract.Change || contract.RollbackMode != RollbackManual {
			t.Fatalf("%s should expose manual rollback metadata: %#v", action, contract)
		}
	}
	for _, action := range []string{"network.interface.description.update", "network.interface.access_vlan.update",
		"network.interface.trunk_vlan.add", "network.interface.trunk_vlan.remove"} {
		contract := ActionSafetyContract(action, RiskHigh)
		if !contract.Change || !contract.SnapshotRequired || contract.RollbackMode != RollbackManual || contract.UnknownResolution != "RESOURCE_REREAD_NO_RETRY" {
			t.Fatalf("%s network change contract = %#v", action, contract)
		}
	}
	for _, action := range []string{"deployment.restart", "statefulset.restart", "cronjob.trigger", "systemd.service.restart", "docker.container.restart"} {
		contract := ActionSafetyContract(action, RiskHigh)
		if !contract.Change || contract.RollbackMode != RollbackUnsupported {
			t.Fatalf("%s should be marked non-rollbackable: %#v", action, contract)
		}
	}
	folderUpdate := ActionSafetyContract("grafana.folder.update", RiskHigh)
	if !folderUpdate.Change || folderUpdate.RollbackMode != RollbackManual {
		t.Fatalf("folder update contract = %#v", folderUpdate)
	}
	create := ActionSafetyContract("huawei.ecs.create", RiskHigh)
	if !create.Change || create.RollbackMode != RollbackUnsupported {
		t.Fatalf("create contract = %#v", create)
	}
	for _, action := range []string{"kubernetes.resource.create", "kubernetes.resource.update", "kubernetes.resource.delete"} {
		contract := ActionSafetyContract(action, RiskHigh)
		if !contract.Change || !contract.SnapshotRequired || contract.RollbackMode != RollbackManual || contract.UnknownResolution != "RESOURCE_REREAD_NO_RETRY" {
			t.Fatalf("%s generic Kubernetes contract = %#v", action, contract)
		}
	}
}

func TestSupportsChangeSafety(t *testing.T) {
	if SupportsChangeSafety([]string{"deployment.get@v1"}) {
		t.Fatal("legacy connector unexpectedly supports change safety")
	}
	if !SupportsChangeSafety([]string{ChangeSafetyCapability}) {
		t.Fatal("change safety capability was not recognized")
	}
}
