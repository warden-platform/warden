# Deployed OPA bundle. Keep in sync with server/internal/policy/policy.rego.
package pam.kubernetes

import rego.v1

base_decision := {
  "decision": "DENY",
  "reason_codes": ["NO_MATCHING_POLICY"],
  "policy_bundle_hash": "development-policy-v1",
  "max_duration_seconds": 60,
  "credential_mode": "KUBERNETES_TOKEN_REQUEST",
  "parameter_constraints": {},
  "obligations": ["AUDIT_FULL"],
}

default decision := {
  "decision": "DENY",
  "reason_codes": ["NO_MATCHING_POLICY"],
  "policy_bundle_hash": "development-policy-v1",
  "max_duration_seconds": 60,
  "credential_mode": "KUBERNETES_TOKEN_REQUEST",
  "parameter_constraints": {},
  "obligations": ["AUDIT_FULL"],
}

testing_actions := {
  "deployment.get",
  "deployment.update_image",
  "deployment.restart",
  "deployment.scale",
  "deployment.rollout_status",
  "pod.logs.read",
  "pod.get",
  "pod.events",
  "pod.metrics",
  "event.list",
  "statefulset.get",
  "statefulset.restart",
  "statefulset.scale",
  "statefulset.update_image",
  "daemonset.get",
  "replicaset.get",
  "job.get",
  "cronjob.get",
  "cronjob.suspend",
  "cronjob.resume",
  "cronjob.trigger",
  "service.get",
  "endpoints.get",
  "ingress.get",
  "configmap.get",
  "node.get",
  "node.metrics",
  "namespace.list",
  "kubernetes.resource.get",
  "kubernetes.resource.list",
  "kubernetes.resource.preview",
  "kubernetes.resource.create",
  "kubernetes.resource.update",
  "kubernetes.resource.delete",
}

readonly_actions := {
  "deployment.get",
  "deployment.rollout_status",
  "pod.logs.read",
  "pod.get",
  "pod.events",
  "pod.metrics",
  "event.list",
  "statefulset.get",
  "daemonset.get",
  "replicaset.get",
  "job.get",
  "cronjob.get",
  "service.get",
  "endpoints.get",
  "ingress.get",
  "configmap.get",
  "node.get",
  "node.metrics",
  "namespace.list",
  "kubernetes.resource.get",
  "kubernetes.resource.list",
  "kubernetes.resource.preview",
}

kubernetes_dashboard_change_actions := {
	"namespace.dashboard_configmap.apply",
	"configmap.dashboard.delete",
}

kubernetes_workload_change_actions := {
	"deployment.update_image",
	"deployment.restart",
	"deployment.scale",
	"statefulset.restart",
	"statefulset.scale",
	"statefulset.update_image",
	"cronjob.suspend",
	"cronjob.resume",
	"cronjob.trigger",
	"kubernetes.resource.create",
	"kubernetes.resource.update",
	"kubernetes.resource.delete",
}

linux_readonly_actions := {
	"linux.host.info",
	"linux.process.list",
	"linux.disk.usage",
	"systemd.service.status",
	"systemd.service.logs",
	"docker.container.list",
	"docker.container.inspect",
	"docker.container.logs",
}

linux_lifecycle_actions := {
	"systemd.service.start",
	"systemd.service.stop",
	"systemd.service.restart",
	"docker.container.start",
	"docker.container.stop",
	"docker.container.restart",
}

jenkins_readonly_actions := {
	"jenkins.controller.get",
	"jenkins.job.get",
	"jenkins.job.config.get",
	"jenkins.build.get",
	"jenkins.build.logs",
	"jenkins.queue.get",
	"jenkins.node.get",
	"jenkins.credential_reference.get",
}

jenkins_change_actions := {
	"jenkins.job.update",
	"jenkins.job.configure",
	"jenkins.job.delete",
	"jenkins.job.enable",
	"jenkins.job.disable",
	"jenkins.job.build",
	"jenkins.build.stop",
	"jenkins.build.retry",
	"jenkins.queue.cancel",
	"jenkins.node.set_offline",
}

jenkins_auto_change_actions := {
	"jenkins.job.create",
}

grafana_readonly_actions := {
	"grafana.instance.get",
	"grafana.folder.list",
	"grafana.folder.get",
	"grafana.dashboard.list",
	"grafana.dashboard.get",
	"grafana.dashboard.export",
	"grafana.datasource.list",
	"grafana.datasource.get",
	"grafana.datasource.query",
}

grafana_change_actions := {
	"grafana.folder.create",
	"grafana.folder.update",
	"grafana.folder.delete",
	"grafana.dashboard.create",
	"grafana.dashboard.update",
	"grafana.dashboard.delete",
	"grafana.datasource.create",
	"grafana.datasource.update",
	"grafana.datasource.delete",
}

database_readonly_actions := {
	"database.instance.get",
	"database.schema.list",
	"database.schema.get",
	"database.table.list",
	"database.table.describe",
	"database.table.rows.query",
	"database.query.preview",
	"database.query.execute",
	"database.query_template.execute",
	"database.schema.create.preview",
	"database.table.create.preview",
}

database_change_actions := {
	"database.schema.create",
	"database.table.create",
}

huawei_readonly_actions := {
	"huawei.account.get",
	"huawei.ecs.get",
	"huawei.ecs.metrics",
	"huawei.rds.get",
	"huawei.elb.get",
	"huawei.obs.bucket.get",
	"huawei.obs.object.list",
	"huawei.obs.object.metadata.get",
	"huawei.lts.logs.query",
}

huawei_change_actions := {
	"huawei.ecs.create",
	"huawei.ecs.start",
	"huawei.ecs.stop",
	"huawei.ecs.reboot",
	"huawei.ecs.update_metadata",
	"huawei.ecs.delete",
	"huawei.obs.object.delete",
}

aliyun_sls_readonly_actions := {
  "aliyun.sls.account.get",
  "aliyun.sls.logs.query",
}

network_readonly_actions := {
  "network.device.get",
  "network.device.audit",
  "network.interface.list",
  "network.vlan.list",
  "network.interface.description.preview",
  "network.interface.access_vlan.preview",
  "network.interface.trunk_vlan.preview",
}

network_change_actions := {
  "network.interface.description.update",
  "network.interface.access_vlan.update",
  "network.interface.trunk_vlan.add",
  "network.interface.trunk_vlan.remove",
}

provider_readonly_actions := {
	"provider.request",
}

provider_change_actions := {
	"provider.write",
}

credential_readonly_actions := {
	"credential.get",
	"credential.list",
}

credential_change_actions := {
	"credential.create",
	"credential.rotate",
	"credential.revoke",
	"credential.bind",
	"credential.unbind",
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["CREDENTIAL_READ_ONLY_ALLOWED"],
	"max_duration_seconds": 60,
	"credential_mode": "PAM_MANAGED_CREDENTIAL",
}) if {
	input.principal.status == "ACTIVE"
	input.action in credential_readonly_actions
}

decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["CREDENTIAL_CHANGE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"credential_mode": "PAM_MANAGED_CREDENTIAL",
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in credential_change_actions
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["GOVERNED_PROVIDER_READ_ALLOWED"],
	"max_duration_seconds": 120,
	"credential_mode": "PROVIDER_CREDENTIAL_REFERENCE",
}) if {
	input.principal.status == "ACTIVE"
	input.action in provider_readonly_actions
}

# provider.write is a governed structured write: it mutates the target
# Integration, so it requires approval + full audit + owner notification via
# the change-safety contract (never ALLOW).
decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["PROVIDER_WRITE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"credential_mode": "PROVIDER_CREDENTIAL_REFERENCE",
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in provider_change_actions
}

decision := object.union(base_decision, {
  "decision": "ALLOW",
  "reason_codes": ["ALIYUN_SLS_READ_ONLY_ALLOWED"],
	"max_duration_seconds": 120,
	"credential_mode": "ALIYUN_SLS_CREDENTIAL_REFERENCE",
}) if {
	input.principal.status == "ACTIVE"
  input.action in aliyun_sls_readonly_actions
}

decision := object.union(base_decision, {
  "decision": "ALLOW",
  "reason_codes": ["NETWORK_READ_ONLY_ALLOWED"],
  "max_duration_seconds": 180,
  "credential_mode": "NETWORK_DEVICE_CREDENTIAL_REFERENCE",
  "obligations": ["AUDIT_FULL"],
}) if {
  input.principal.status == "ACTIVE"
  input.connector.type == "network"
  input.action in network_readonly_actions
}

decision := object.union(base_decision, {
  "decision": "REQUIRE_APPROVAL",
  "reason_codes": ["NETWORK_CHANGE_REQUIRES_APPROVAL"],
  "max_duration_seconds": 180,
  "credential_mode": "NETWORK_DEVICE_CREDENTIAL_REFERENCE",
  "obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
  input.principal.status == "ACTIVE"
  input.connector.type == "network"
  input.action in network_change_actions
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["NON_PRODUCTION_LINUX_LIFECYCLE_ALLOWED"],
	"max_duration_seconds": 300,
	"credential_mode": "CONNECTOR_LOCAL_CONTROLLED",
	"obligations": ["AUDIT_FULL"],
}) if {
	input.principal.status == "ACTIVE"
	input.connector.type == "linux"
	input.environment in {"development", "testing"}
	input.action in linux_lifecycle_actions
}

decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["PRODUCTION_LINUX_LIFECYCLE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"credential_mode": "CONNECTOR_LOCAL_CONTROLLED",
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.connector.type == "linux"
	input.environment == "production"
	input.action in linux_lifecycle_actions
}

decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["KUBERNETES_DASHBOARD_CONFIG_CHANGE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in kubernetes_dashboard_change_actions
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["LINUX_READ_ONLY_ALLOWED"],
	"max_duration_seconds": 120,
	"credential_mode": "CONNECTOR_LOCAL_READ_ONLY",
}) if {
	input.principal.status == "ACTIVE"
	input.connector.type == "linux"
	input.action in linux_readonly_actions
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["JENKINS_READ_ONLY_ALLOWED"],
	"max_duration_seconds": 120,
	"credential_mode": "JENKINS_CREDENTIAL_REFERENCE",
}) if {
	input.principal.status == "ACTIVE"
	input.action in jenkins_readonly_actions
}

# Creating a Pipeline Job is a governed mutation, but it is allowed to execute
# without a human approval. Scope validation, single-use grants, change-safety
# verification and full audit still apply.
decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["JENKINS_JOB_CREATE_ALLOWED"],
	"max_duration_seconds": 300,
	"credential_mode": "JENKINS_CREDENTIAL_REFERENCE",
	"obligations": ["AUDIT_FULL"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in jenkins_auto_change_actions
}

decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["JENKINS_CHANGE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"credential_mode": "JENKINS_CREDENTIAL_REFERENCE",
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in jenkins_change_actions
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["GRAFANA_READ_ONLY_ALLOWED"],
	"max_duration_seconds": 120,
	"credential_mode": "GRAFANA_CREDENTIAL_REFERENCE",
}) if {
	input.principal.status == "ACTIVE"
	input.action in grafana_readonly_actions
}

decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["GRAFANA_CHANGE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"credential_mode": "GRAFANA_CREDENTIAL_REFERENCE",
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in grafana_change_actions
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["DATABASE_READ_ONLY_ALLOWED"],
	"max_duration_seconds": 120,
	"credential_mode": "DATABASE_CREDENTIAL_REFERENCE",
}) if {
	input.principal.status == "ACTIVE"
	input.action in database_readonly_actions
}

decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["DATABASE_CHANGE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"credential_mode": "DATABASE_CREDENTIAL_REFERENCE",
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in database_change_actions
}

decision := object.union(base_decision, {
	"decision": "ALLOW",
	"reason_codes": ["HUAWEI_CLOUD_READ_ONLY_ALLOWED"],
	"max_duration_seconds": 120,
	"credential_mode": "HUAWEI_CLOUD_CREDENTIAL_REFERENCE",
}) if {
	input.principal.status == "ACTIVE"
	input.action in huawei_readonly_actions
}

decision := object.union(base_decision, {
	"decision": "REQUIRE_APPROVAL",
	"reason_codes": ["HUAWEI_CLOUD_CHANGE_REQUIRES_APPROVAL"],
	"max_duration_seconds": 300,
	"credential_mode": "HUAWEI_CLOUD_CREDENTIAL_REFERENCE",
	"obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
	input.principal.status == "ACTIVE"
	input.action in huawei_change_actions
}

decision := object.union(base_decision, {
  "decision": "ALLOW",
  "reason_codes": ["NON_PRODUCTION_ACTION_ALLOWED"],
  "max_duration_seconds": 300,
}) if {
  input.principal.status == "ACTIVE"
  input.environment in {"development", "testing"}
  input.action in testing_actions
}

decision := object.union(base_decision, {
  "decision": "ALLOW",
  "reason_codes": ["PRODUCTION_READ_ALLOWED"],
  "max_duration_seconds": 120,
}) if {
  input.principal.status == "ACTIVE"
  input.environment == "production"
  input.action in readonly_actions
}

decision := object.union(base_decision, {
  "decision": "REQUIRE_APPROVAL",
  "reason_codes": ["PRODUCTION_WRITE_REQUIRES_APPROVAL"],
  "max_duration_seconds": 300,
  "obligations": ["AUDIT_FULL", "NOTIFY_OWNER"],
}) if {
  input.principal.status == "ACTIVE"
  input.environment == "production"
  input.action in kubernetes_workload_change_actions
}
