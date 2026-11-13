---
name: pam-platform-access
description: Discover PAM-managed infrastructure, inspect live action contracts, run governed read or change executions, and report approval and safety results. Use for Kubernetes, Linux, Docker, databases, Jenkins, Grafana, Huawei Cloud, Alibaba Cloud, resource inventory, operational diagnosis, or any request that must access infrastructure through PAM instead of direct credentials.
---

# PAM Platform Access

Use only `${PAM_HOME:-.}/scripts/pam-agent.sh`. Read `PAM_API_ENDPOINT` and `PAM_AGENT_API_KEY` from the injected environment; never print, inspect, forward, or persist them.

## Platform model

PAM exposes infrastructure through one governed chain:

```text
Project / Environment
└── Resource → live Action contract → Execution → optional Approval → Connector → audited Result
```

- A Resource is an already discovered target such as a Deployment, Linux service, Jenkins Job, database table, Grafana Dashboard, or cloud instance.
- An Action contract supplies the current version, risk, execution mode, timeout, and JSON parameter Schema.
- An Execution binds one principal, resource, action version, parameter hash, reason, and idempotency key.
- Policy decides whether the Execution runs directly or waits for approval. The Connector executes with scoped credentials and returns structured evidence.
- PAM is not a general remote shell or raw API proxy. If an operation is absent from the selected Resource's action catalog, it is unsupported for that target.

## Boundary

- Treat PAM as the only infrastructure access path. Never substitute `kubectl`, SSH, cloud CLIs, database clients, service APIs, raw SQL, arbitrary shell, kubeconfig, or long-lived credentials.
- Use repository tools only for source code. Do not confuse source access with permission to access live infrastructure.
- Select only `ACTIVE` resources returned by PAM. Never invent resource IDs, Regions, names, projects, environments, action versions, or parameters.
- Call `list-actions RESOURCE_ID` immediately before execution. Send only fields declared by its current `parameter_schema`; do not add `name` or `namespace` unless the Schema declares them.
- Use project, environment, resource type, and Integration to disambiguate duplicate names.
- Treat only `SUCCEEDED` as completed. For `WAITING_APPROVAL`, report the Execution ID and wait after approval. For `UNKNOWN`, reread the Execution and target state; never automatically replay a mutation.
- Never approve on the user's behalf, relax policy, expose secrets, or bypass a PAM denial.

## Discover the target

Start with the smallest useful query:

```bash
${PAM_HOME:-.}/scripts/pam-agent.sh resource-summary [PROJECT] [ENVIRONMENT] [RESOURCE_TYPE]
${PAM_HOME:-.}/scripts/pam-agent.sh find-resources QUERY [PROJECT] [ENVIRONMENT] [RESOURCE_TYPE]
${PAM_HOME:-.}/scripts/pam-agent.sh list-resources [RESOURCE_TYPE]
```

Use `resource-summary` for inventories, `find-resources` for a known target, and `list-resources` only when a full type list is necessary. Confirm the exact resource identity before continuing.

## Execute through the live contract

Read the selected resource's action catalog:

```bash
${PAM_HOME:-.}/scripts/pam-agent.sh list-actions RESOURCE_ID
```

For a declared read action:

```bash
${PAM_HOME:-.}/scripts/pam-agent.sh create-readonly-execution ACTION RESOURCE_ID 'SCHEMA_VALID_JSON' 'REASON'
${PAM_HOME:-.}/scripts/pam-agent.sh wait-readonly-result EXECUTION_ID 60
```

For a supported governed change:

```bash
${PAM_HOME:-.}/scripts/pam-agent.sh create-aiops-execution ACTION RESOURCE_ID 'SCHEMA_VALID_JSON' 'REASON'
${PAM_HOME:-.}/scripts/pam-agent.sh wait-result EXECUTION_ID 300
```

Inspect the create response before waiting. If it is `WAITING_APPROVAL`, report it and call `wait-result` only after approval is confirmed; never keep a turn blocked waiting for an approval that has not happened.

Reuse `PAM_IDEMPOTENCY_KEY` only when retrying the same HTTP request. Never create a new idempotency key to replay an `UNKNOWN` change.

## Interpret results

- Report the Execution ID, exact resource identity, action, status, and minimal evidence.
- For mutations, summarize `_pam_safety.before`, `_pam_safety.after`, and `_pam_safety.verification`.
- `VERIFIED_ABSENT` confirms a deleted resource could no longer be read.
- `ACCEPTED_ASYNC` confirms the provider accepted an asynchronous operation; continue read-only observation until the catalog converges, without resubmitting it.
- An empty query result proves only that no matching data was returned for the stated scope and time window.
- Redact authorization data, cookies, tokens, secret values, signed URLs, and unrelated personal information.

## Platform capabilities

- Read [references/platform-capabilities.md](references/platform-capabilities.md) when routing a request to Kubernetes, Linux/Docker, Jenkins, Grafana, databases, Huawei Cloud, or Alibaba Cloud.
- Treat that reference as orientation only. The selected Resource's live action catalog and Schema always override examples or remembered capabilities.
