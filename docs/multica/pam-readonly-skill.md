---
name: pam-readonly
description: Discover and inspect Warden-managed Kubernetes, Linux/Docker, Jenkins, Grafana, PostgreSQL/MySQL, and Huawei Cloud resources through a strictly read-only workflow. Use Warden's authoritative project/environment ownership and never bypass Warden or submit mutations.
---

# Warden ReadOnly v2

Use this Skill for every infrastructure lookup. Warden is the only permitted access path.

## Client

```bash
${WARDEN_HOME:-.}/scripts/warden-agent.sh
```

`WARDEN_API_ENDPOINT` and `WARDEN_AGENT_API_KEY` are injected. Never print, persist, forward, or inspect their values.

The Key is bound to one organization (tenant): discovery and reads are scoped to it, and the agent sees only its own executions. Project membership roles govern execution rights (`VIEWER` is read-only); reads never require a non-VIEWER role.

## Absolute safety boundary

- Never run `kubectl`, `ssh`, `docker`, `systemctl`, `journalctl`, `psql`, `mysql`, cloud CLIs, Jenkins/Grafana direct APIs, `sudo`, container exec, raw SQL, or direct infrastructure `curl`.
- Never request or reveal kubeconfigs, ServiceAccount tokens, API keys, passwords, Secret values, Docker sockets, or credential material.
- Use only `create-readonly-execution`; never use the generic `create-execution` command.
- Mutations are outside this Agent's role even when Warden supports approval. Refuse create/update/delete/enable/disable/restart/build/retry/stop/deploy actions.
- The client-side allowlist is a second enforcement layer. If it rejects an action, stop and report the refusal.
- Only use resources returned by the executable-resource commands. Never invent IDs, action names, versions, or parameters.
- Treat `SUCCEEDED` as success. Report `FAILED`, `CANCELLED`, `UNKNOWN`, `EXPIRED`, unexpected `WAITING_APPROVAL`, authentication failures, and offline channels without bypass or blind retry.

## Efficient discovery workflow

### Broad inventory or topology request

Start with a compact authoritative summary instead of loading every resource row:

```bash
${WARDEN_HOME:-.}/scripts/warden-agent.sh resource-summary [PROJECT] [ENVIRONMENT] [RESOURCE_TYPE]
```

Arguments are optional exact filters. `ENVIRONMENT` is `development`, `testing`, or `production`.

The summary groups by project, environment, resource type, and inheritance source. Use it for questions such as “公司有哪些资源”“生产环境有哪些数据库”“某项目有哪些服务”. A directory-only answer has no Execution ID; say so explicitly.

### Find a concrete target

```bash
${WARDEN_HOME:-.}/scripts/warden-agent.sh find-resources QUERY [PROJECT] [ENVIRONMENT] [RESOURCE_TYPE]
```

This returns only minimal identity and governance fields. If multiple rows match, disambiguate using:

1. `project_name`
2. `environment`
3. `resource_type`
4. `connector_id`
5. `integration_id`
6. `namespace` / `external_id`

Do not infer ownership from a resource name, Namespace, hostname, IP, or prior knowledge.

Use raw `list-resources [type]` only when the compact commands cannot provide a field required by the selected action. Do not paste the raw catalog into the final answer.

## Project infrastructure overview mode

For project onboarding, inventory, or “what resources do we have” requests, summarize where each project runs instead of dumping the catalog. Do not create an Execution.

1. Use `resource-summary` to discover the project set, then inspect `node`, `linux_host`, `huawei_ecs_instance`, `jenkins_controller`, and `grafana_instance`. To add representative resources, read Namespace/Deployment, systemd/Docker children, `jenkins_job`, and `grafana_dashboard`, but show at most three from each category.
2. A `node` is not a named cluster. Use `connector_id` only to separate execution channels and never print it. If Warden does not expose a cluster name, say `cluster name not exposed`; never substitute the Node count for the cluster name.
3. An ECS name or label containing `k8s` or `CCE` is only a possible node hint. Without Kubernetes objects or a confirmed execution-channel name, label the conclusion as inferred instead of claiming an attached production cluster.
4. Count only `linux_host` and `huawei_ecs_instance` as servers. Do not count containers, systemd services, ELB, RDS, OBS, or other descendants as servers.
5. Derive the hosted business from Namespace, workload, project tag, or resource metadata. Mark a purpose inferred only from a hostname as `name-based inference`.
6. For Jenkins and Grafana, show the controller/instance address plus at most three representative Jobs or Dashboards. Omit Builds, aggregate child counts, and full child lists. Mark an endpoint shared only when descendants from the same Integration are assigned to multiple projects.
7. Count Nodes only within the same project, environment, and execution channel. Show the Node count without using it as a substitute for the cluster name, and summarize at most three hosted businesses from Namespace/Deployment evidence.
8. List at most three business services for a server only when systemd/Docker children can be associated with that host. For cloud ECS without host-level collection, write `internal services not collected`; never invent services from the hostname.

Multica Issue pages are narrow. Never use an inventory table with four or more columns or put a nested list inside a cell. For every project, render one independent two-column table:

#### `<project name>`

| Item | Content |
|---|---|
| Cluster | Name, environment, Node count, and at most three hosted businesses; write `cluster name not exposed` when unavailable |
| Servers | Count, purpose groups, and at most three actually collected business services; state when internal services are not collected |
| Jenkins | Address, sharing boundary, and at most three representative Jobs |
| Grafana | Address, sharing boundary, and at most three representative Dashboards |
| Main business | One sentence without leaf-resource details |

Sort projects by name and use `—` for an item Warden has not discovered. Group more than ten servers by confirmed purpose instead of listing them. After all project tables, add no more than three global notes for shared boundaries, explicit inferences, and data gaps. Omit query steps, evidence sections, confidence, Execution IDs, Region distributions, and child-resource counts unless requested. Use the same two-column format for a single project and keep it readable in one minute.

## Governance fields

Project ownership is authoritative:

| `project_source` | Meaning |
|---|---|
| `RESOURCE` | Single-resource override |
| `INTEGRATION_RULE` | Jenkins Integration routing rule |
| `CLOUD_TAG` | Warden-owned cloud project tag |
| `NAMESPACE` | Kubernetes Namespace rule |
| `INTEGRATION` | Integration default project |
| `CONNECTOR` | Connector project for native resources |
| `DEFAULT` | System default project |

Environment ownership is authoritative:

| `environment_source` | Meaning |
|---|---|
| `RESOURCE` | Single-resource override |
| `DATABASE_SCHEMA` | Table inherits its Schema |
| `DATABASE_INSTANCE` | Schema/table inherits the database instance |
| `INTEGRATION` | Integration default environment |
| `CONNECTOR` | Execution-channel environment |

Database precedence is:

```text
table override > Schema override > database instance override > Integration > Connector
```

Always report both the effective value and its source when environment or project context matters.

## Execute a read

First load the live action contract for the chosen resource:

```bash
${WARDEN_HOME:-.}/scripts/warden-agent.sh list-actions RESOURCE_ID
```

Copy the exact action `name`, `version`, and required parameters from the response. Parameters must match the selected resource identity.

Then create and wait:

```bash
${WARDEN_HOME:-.}/scripts/warden-agent.sh create-readonly-execution ACTION RESOURCE_ID '{"parameter":"value"}' 'Short reason tied to the user request'
${WARDEN_HOME:-.}/scripts/warden-agent.sh wait-readonly-result EXECUTION_ID 60
```

Use a longer timeout only for bounded log/metric reads that are known to need it. Metadata, bounded logs/metrics, and structured database row queries are audited reads and should not enter approval. If a read returns `WAITING_APPROVAL`, report it as a policy misconfiguration instead of bypassing Warden or waiting for a human. Never reinterpret `WAITING_APPROVAL` as missing capability.

## Supported read families

The live `list-actions RESOURCE_ID` response is always authoritative. Current read families include:

- Kubernetes: `*.get`, `*.list`, `deployment.rollout_status`, `pod.events`, `pod.logs.read`, `pod.metrics`, `node.metrics` for Deployment, Pod, Event, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob, Service, Endpoints, Ingress, ConfigMap, Node, and Namespace.
- Linux/Docker: host info, process list, disk usage, systemd status/logs, Docker container list/inspect/logs.
- Jenkins: controller, Job, Job config, Build/logs, Node, queue item, and credential-reference metadata reads. Never reveal credential values.
- Grafana: instance/folder/dashboard/datasource reads, dashboard export, and bounded datasource query when the Integration exists and Warden exposes the action.
- Database: instance info, Schema list/get, table list/describe, and bounded `database.table.rows.query`. Row queries must select explicit columns and use structured filters/order; raw SQL, joins, DML, DDL and dumps remain forbidden.
- Huawei Cloud: account/region capability, ECS detail/metrics, RDS detail, ELB detail, OBS bucket usage, and bounded LTS log query. `huawei.ecs.create` is forbidden.
- Aliyun SLS: RAM-account discovery across authorized Regions, Project/Logstore hierarchy, and bounded read-only log query.

Secret enumeration and every mutation are intentionally excluded from the ReadOnly client allowlist. Warden does not publish a `secret.list` action because Kubernetes would return Secret data to the Connector even when the UI displays metadata only.

For a database row query, first describe the table, then request only the columns needed for the incident. Example parameters (the `name` and `namespace` values must exactly match the selected resource):

```json
{
  "name": "user_credits_bill",
  "namespace": "public",
  "columns": ["uid", "event_no", "amount", "request_id", "cst_created"],
  "filters": [
    {"column": "uid", "operator": "in", "value": [16035793, 16016006]},
    {"column": "cst_created", "operator": "gte", "value": "2026-07-16 00:00:00"}
  ],
  "orderBy": [{"column": "cst_created", "direction": "desc"}],
  "limit": 100
}
```

Supported filter operators are `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `like`, `in`, `is_null`, and `is_not_null`; filters are combined with `AND`. Use `offset` only for bounded follow-up pages. Minimize personal-data columns and never return unrelated rows.

## Output contract

For an executed read, return:

1. Scope: project, environment, resource type.
2. Target: name, namespace/external identity, Connector and Integration when relevant.
3. Governance: effective project/environment and both sources.
4. Execution: Execution ID and terminal status.
5. Result: concise structured facts needed to answer the question.

For inventory-only queries, return grouped counts and explicitly state that no Execution was created.

Do not dump full catalogs or logs. Redact obvious tokens, passwords, authorization headers, signed URLs, credential references, Secret names, and unrelated internal endpoints. Only reveal an address when the user explicitly requests it and the Warden result legitimately contains it.
