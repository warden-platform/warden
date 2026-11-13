---
name: pam-aiops
description: Diagnose PAM-managed Kubernetes and observability resources and govern Grafana dashboards through bounded, audited PAM actions without direct infrastructure credentials.
---

# PAM AIOps

PAM is the only infrastructure access path. Use `${PAM_HOME:-.}/scripts/pam-agent.sh`; credentials are injected as `PAM_API_ENDPOINT` and `PAM_AGENT_API_KEY` and must never be printed or inspected.

## Hard boundary

- Never invoke `kubectl`, cloud CLIs, Grafana/Prometheus/Loki/SLS/LTS direct APIs, SSH, database clients, or infrastructure credentials.
- Discover an executable resource, load its live action contract, and copy the exact action version and parameters.
- Use `create-aiops-execution`; its local allowlist is a second enforcement layer.
- Metadata/log/metric reads must normally complete without approval. A structured database row query may require approval because it can expose business or personal data; report the Access Request and continue only after approval. Every Grafana or Dashboard ConfigMap mutation must enter PAM approval; never bypass, duplicate blindly, or claim success before `SUCCEEDED`.
- No application deploy/rollout/scale, arbitrary ConfigMap/Secret mutation, arbitrary cloud API, Grafana secret datasource fields, or datasource type outside Prometheus/Loki.

## Diagnostic flow

1. Resolve the service profile from `aiops-commander-skill`.
2. Use `resource-summary` and `find-resources` with the authoritative PAM project/environment.
3. Kubernetes evidence: structured workload reads, Pod logs/events, namespace events, and `pod.metrics`/`node.metrics` when metrics-server is available.
4. Metrics/logs: use `grafana.datasource.query`, `huawei.lts.logs.query`, or `aliyun.sls.logs.query` against the exact discovered resource. Huawei LTS queries target a discovered `huawei_lts_log_stream`; never ask the user to provide group/stream IDs. Aliyun SLS queries target a discovered `aliyun_sls_logstore`. Cloud retention remains authoritative; historical range is unrestricted, while each response stays at or below 500 rows and 4 MiB. Raw cloud-log results expire from PAM after 10 minutes.
5. Database evidence: describe the selected table, then use `database.table.rows.query` with explicit columns and structured filters. Query only incident-related rows; raw SQL, joins, DML, DDL and dumps are forbidden.
6. Correlate timestamps and request/trace IDs; missing/partial data is not evidence of health.

## Governance flow

The only permitted writes are:

- Grafana Folder/Dashboard create, update, delete.
- Prometheus/Loki Datasource create, update, delete without `secureJsonData`.
- `namespace.dashboard_configmap.apply` and `configmap.dashboard.delete` for `grafana-dashboard-*` resources.

Before submitting a write, state the target, intended change, rollback/reconciliation read, and reason. Wait for approval and terminal state, then re-read the target. `UNKNOWN` means the mutation may have occurred; reconcile instead of retrying.

## Commands

```bash
scripts/pam-agent.sh list-actions RESOURCE_ID
scripts/pam-agent.sh create-aiops-execution ACTION RESOURCE_ID PARAMS_JSON REASON
scripts/pam-agent.sh wait-result EXECUTION_ID 300
```

Return a concise report with scope, resource identity, project/environment sources, Execution ID, terminal state, evidence, gaps, and confidence. Redact credentials, authorization data, cookies, signed URLs, personal data, and unrelated log fields.
