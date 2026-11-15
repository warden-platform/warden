# Warden Platform Capabilities

Use this reference to choose a resource type and understand domain-specific safety constraints. Always confirm the exact capability with `list-actions RESOURCE_ID` before execution.

## Kubernetes

Typical resources include Namespace, Deployment, StatefulSet, DaemonSet, CronJob, Job, Pod, Service, Ingress, ConfigMap, Node, and Event.

- Reads cover object detail, rollout state, events, bounded Pod logs, and Pod/Node metrics.
- Governed changes may include restart, scale, image update, CronJob suspend/resume/trigger, and allowlisted dashboard ConfigMap operations.
- Fix Namespace, resource name, container, and expected generation to the selected catalog resource. Never submit arbitrary YAML, Patch, commands, or cross-Namespace identities.

## Linux, systemd, and Docker

Typical resources include `linux_host`, `systemd_service`, and `docker_container`.

- Reads cover host information, process and disk summaries, service status/logs, running containers, container detail, and bounded logs.
- Governed changes cover allowlisted service/container start, stop, and restart.
- Never use SSH, sudo, shell, `systemctl`, `journalctl`, Docker CLI/Socket, container exec, image pulls, or arbitrary arguments.

## Jenkins

Typical resources include controller, Job, Build, queue item, node, and credential reference.

- Reads cover controller/Job/Build state, bounded logs, queue state, nodes, and credential identities.
- Governed changes may cover build trigger/stop/retry, structured Job creation/configuration, enable/disable, queue cancellation, and node offline state.
- Never call Jenkins directly or use raw Job XML, arbitrary Groovy/Pipeline text, passwords, Tokens, or credential Secret values. A create conflict must not become an update.

## Grafana and observability

Typical resources include Grafana instance, Folder, Dashboard, and Datasource.

- Reads cover Folder/Dashboard/Datasource discovery, Dashboard export, and bounded Datasource queries such as Prometheus or Loki.
- Governed changes may cover structured Folder, Dashboard, Datasource, and dashboard ConfigMap governance.
- Never use a Grafana Token directly, invent Datasource queries outside the Schema, or expose credentials embedded in exported data.

## Databases

Typical resources include database instance, Schema, and table.

- Reads cover instance metadata, Schema/table discovery, table structure, and structured bounded row queries.
- Use explicit fields, AND-only filters, allowlisted ordering, and bounded limits from the live Schema.
- Never send raw SQL, mutations, joins, stored procedure calls, or database credentials. Query multiple tables separately and correlate results in the Agent when necessary.

## Huawei Cloud

Typical resources include regional cloud account, ECS, RDS, ELB, OBS bucket, and LTS log resources.

- Reads cover account capability, ECS detail/metrics, RDS/ELB detail, OBS metadata, and bounded LTS queries.
- ECS creation targets an exact `huawei_cloud_account` Region and requires the live Schema's Warden project, Region, image, flavor, network, and security group fields.
- ECS lifecycle and metadata actions target one discovered `huawei_ecs_instance`. Reserved `pam-*` tags come only from trusted Warden context.
- ECS deletion requires an exact `confirmServerName` match and explicit public-IP/volume choices. Submit once. `VERIFIED_ABSENT` confirms immediate absence; `ACCEPTED_ASYNC` requires read-only catalog observation and must never be replayed automatically.

## Alibaba Cloud

Typical resources include SLS regional account, Project, and Logstore.

- Reads cover account capability, resource discovery, and bounded SLS log queries.
- Fix Region, Project, Logstore, time window, and query parameters to the selected resource and live Schema.
- Never use Alibaba Cloud CLI, AK/SK, arbitrary OpenAPI calls, or an unbounded log query.

## Inventory and project guide

For “what resources does this project have?” requests, use `resource-summary` first and avoid creating Executions. Group by project, environment, and resource type; distinguish discovered infrastructure from inferred business purpose. Do not treat a node as a named cluster, a container/service as a server, or absence from Warden as proof that the real-world resource does not exist.
