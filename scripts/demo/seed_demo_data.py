#!/usr/bin/env python3
"""Seed a self-contained demo tenant ("acme") into the local Warden database.

Creates: tenant + demo admin login, projects, agents & API keys, connectors,
integrations, resources, policy rules, ~8h of executions, pending approvals
and an audit trail — enough to populate every console page with realistic
data. Idempotent: deterministic UUIDs + ON CONFLICT DO NOTHING.

Usage:
  python3 scripts/demo/seed_demo_data.py           # seed (skip existing rows)
  python3 scripts/demo/seed_demo_data.py --reset   # delete demo tenant data, reseed

Demo logins (tenant "acme"): demo / zhangsan / lisi, all with password
"12345678" — deliberately independent of the built-in admin password.
Requires psql (Homebrew keg path is auto-detected) and database settings in
config.yaml.
"""
import hashlib
import json
import os
import shutil
import subprocess
import sys
import uuid
from datetime import datetime, timedelta, timezone
from urllib.parse import urlparse

from local_config import ROOT, database_url, local_setting

NS = uuid.UUID('decafbad-0000-4000-8000-000000000001')  # deterministic demo namespace


def uid(name: str) -> str:
    return str(uuid.uuid5(NS, name))


def find_psql() -> str:
    found = shutil.which('psql')
    if found:
        return found
    for cand in ('/opt/homebrew/opt/postgresql@18/bin/psql', '/opt/homebrew/opt/postgresql@17/bin/psql', '/opt/homebrew/bin/psql'):
        if os.path.exists(cand):
            return cand
    sys.exit('psql not found; install PostgreSQL client tools first')


def sql_str(v: str) -> str:
    return "'" + v.replace("'", "''") + "'"


def j(v) -> str:
    return sql_str(json.dumps(v, ensure_ascii=False)) + '::jsonb'


NOW = datetime.now(timezone.utc)


def ts(minutes_ago: float) -> str:
    return (NOW - timedelta(minutes=minutes_ago)).isoformat()


# bcrypt hash of the public demo password "12345678" (cost 10).
DEMO_PASSWORD_HASH = '$2a$10$2qFZIPAZp.FzSnTOh3Ie2uooLvBrvPOf0mRAhhsLDCQIa77UvB7Tu'

TENANT = 'acme'
TID = uid('tenant:acme')
DEMO = uid('user:demo')
PROJECTS = {name: uid(f'project:{name}') for name in ('payments', 'data-platform', 'infra')}
AGENTS = {name: uid(f'agent:{name}') for name in ('ops-copilot', 'sre-agent-01', 'db-analyst', 'ci-agent', 'cloud-bot')}
REGISTRY = uid('agent_type:multica-registry')
CONNECTORS = {name: uid(f'connector:{name}') for name in ('k8s-prod-01', 'k8s-staging-01', 'linux-bastion-02', 'jenkins-ci', 'db-proxy-01', 'h3c-core-sw')}
INTEGRATIONS = {name: uid(f'integration:{name}') for name in ('jenkins-acme', 'grafana-prod', 'mysql-prod')}

out = []
w = out.append


def insert(table, cols, rows, conflict='DO NOTHING'):
    if not rows:
        return
    w(f'INSERT INTO {table} ({", ".join(cols)}) VALUES')
    w(',\n'.join('(' + ', '.join(r) + ')' for r in rows))
    w(f'ON CONFLICT {conflict};')


# ---------------------------------------------------------------- tenant

if '--reset' in sys.argv:
    w(f"DELETE FROM audit_events WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM access_requests WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM database_previews WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM executions WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM policy_decisions WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM resource_actions WHERE resource_id IN (SELECT id FROM resources WHERE tenant_id={sql_str(TENANT)});")
    w(f"DELETE FROM resource_project_bindings WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM resources WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM integrations WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM connectors WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM policy_matrix_rules WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM project_memberships WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM projects WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM principal_api_keys WHERE principal_id IN (SELECT id FROM principals WHERE tenant_id={sql_str(TENANT)});")
    w(f"DELETE FROM admin_sessions WHERE principal_id IN (SELECT id FROM principals WHERE tenant_id={sql_str(TENANT)});")
    w(f"DELETE FROM principals WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM tenant_quotas WHERE tenant_id={sql_str(TENANT)};")
    w(f"DELETE FROM tenants WHERE id={sql_str(TID)};")

insert('tenants', ['id', 'slug', 'name', 'status', 'settings', 'created_at', 'updated_at'],
       [[sql_str(TID), sql_str(TENANT), sql_str('Acme 科技'), "'ACTIVE'", j({'demo': True}), sql_str(ts(60 * 24 * 30)), sql_str(ts(0))]])
# tenant_quotas keys by tenant SLUG (the list API joins on t.slug).
# Only the concurrency safety valve remains — no commercial metering columns.
insert('tenant_quotas', ['tenant_id', 'max_concurrent_tasks'],
       [[sql_str(TENANT), '100']])

# ---------------------------------------------------------------- principals
insert('principals', ['id', 'kind', 'name', 'status', 'tenant_id', 'metadata', 'password_hash', 'last_login_at', 'created_at', 'updated_at'],
       [[sql_str(DEMO), "'user'", sql_str('demo'), "'ACTIVE'", sql_str(TENANT),
         j({'role': 'tenant_admin', 'display_name': '演示管理员', 'email': 'demo@acme.io'}), sql_str(DEMO_PASSWORD_HASH), sql_str(ts(30)), sql_str(ts(60 * 24 * 20)), sql_str(ts(0))]])
agent_meta = {
    'ops-copilot': ('运维副驾', 'ma-ops-001'),
    'sre-agent-01': ('SRE 值守', 'ma-sre-001'),
    'db-analyst': ('数据分析', 'ma-db-001'),
    'ci-agent': ('CI 流水线', 'ma-ci-001'),
    'cloud-bot': ('云资源巡检', 'ma-cloud-001'),
}
rows = []
for name, pid in AGENTS.items():
    label, ma = agent_meta[name]
    rows.append([sql_str(pid), "'agent_instance'", sql_str(name), "'ACTIVE'", sql_str(TENANT),
                 j({'display_name': label, 'platform': 'multica', 'workspace_id': 'ws-acme-ops', 'multica_agent_id': ma}),
                 'NULL', 'NULL', sql_str(ts(60 * 24 * 15)), sql_str(ts(0))])
for uname, dname in (('zhangsan', '张三'), ('lisi', '李四')):
    rows.append([sql_str(uid(f'user:{uname}')), "'user'", sql_str(uname), "'ACTIVE'", sql_str(TENANT),
                 j({'role': 'project_user', 'display_name': dname, 'email': f'{uname}@acme.io'}), sql_str(DEMO_PASSWORD_HASH), 'NULL', sql_str(ts(60 * 24 * 10)), sql_str(ts(0))])
rows.append([sql_str(REGISTRY), "'agent_type'", sql_str('multica-registry'), "'ACTIVE'", sql_str(TENANT),
             j({'display_name': 'Multica 注册机器人', 'platform': 'multica'}), 'NULL', 'NULL', sql_str(ts(60 * 24 * 15)), sql_str(ts(0))])
insert('principals', ['id', 'kind', 'name', 'status', 'tenant_id', 'metadata', 'password_hash', 'last_login_at', 'created_at', 'updated_at'], rows)

# ---------------------------------------------------------------- api keys
key_rows = []
KEYS = {}
for name, pid in AGENTS.items():
    kid = uid(f'key:{name}:primary')
    KEYS[name] = kid
    digest = hashlib.sha256(f'demo-key-{name}'.encode()).digest()
    key_rows.append([sql_str(kid), sql_str(pid), sql_str('default-key'), sql_str('wdn_agt_' + name[:4]),
                     sql_str('\\x' + digest.hex()), "'ACTIVE'", j(['pam:execution:create', 'pam:execution:read']),
                     sql_str(ts(-60 * 24 * 60)), sql_str(ts(5)), "'10.20.0.8'", sql_str(DEMO), sql_str(ts(60 * 24 * 10)), 'NULL', 'NULL', 'NULL', "'AGENT'", 'NULL'])
revoked = uid('key:ops-copilot:old')
key_rows.append([sql_str(revoked), sql_str(AGENTS['ops-copilot']), sql_str('legacy-key'), sql_str('wdn_agt_old0'),
                 sql_str('\\x' + hashlib.sha256(b'demo-key-old').digest().hex()), "'REVOKED'", j(['pam:execution:create']),
                 'NULL', sql_str(ts(60 * 24 * 3)), "'10.20.0.8'", sql_str(DEMO), sql_str(ts(60 * 24 * 40)), sql_str(ts(60 * 24 * 2)), sql_str(DEMO), 'NULL', "'AGENT'", 'NULL'])
reg_key = uid('key:multica-registry:reg')
key_rows.append([sql_str(reg_key), sql_str(REGISTRY), sql_str('registration'), sql_str('wdn_reg_mult'),
                 sql_str('\\x' + hashlib.sha256(b'demo-key-reg').digest().hex()), "'ACTIVE'", j(['pam:registration:write']),
                 sql_str(ts(-60 * 24 * 90)), 'NULL', 'NULL', sql_str(DEMO), sql_str(ts(60 * 24 * 15)), 'NULL', 'NULL', 'NULL', "'REGISTRATION'", sql_str('multica')])
insert('principal_api_keys', ['id', 'principal_id', 'name', 'key_prefix', 'key_hash', 'status', 'scopes', 'expires_at', 'last_used_at', 'last_used_ip', 'created_by', 'created_at', 'revoked_at', 'revoked_by', 'rotation_parent_id', 'purpose', 'audience'], key_rows)

# ---------------------------------------------------------------- projects
insert('projects', ['id', 'name', 'description', 'is_default', 'tenant_id', 'created_at', 'updated_at'], [
    [sql_str(PROJECTS['payments']), sql_str('payments'), sql_str('支付业务线 · 生产环境'), 'true', sql_str(TENANT), sql_str(ts(60 * 24 * 20)), sql_str(ts(0))],
    [sql_str(PROJECTS['data-platform']), sql_str('data-platform'), sql_str('数据平台 · 数据库与分析'), 'false', sql_str(TENANT), sql_str(ts(60 * 24 * 18)), sql_str(ts(0))],
    [sql_str(PROJECTS['infra']), sql_str('infra'), sql_str('基础设施 · 主机与网络'), 'false', sql_str(TENANT), sql_str(ts(60 * 24 * 18)), sql_str(ts(0))],
])
mem_rows = [[sql_str(TENANT), sql_str(p), sql_str(DEMO), "'PROJECT_ADMIN'", "'ACTIVE'", sql_str(DEMO), sql_str(ts(60 * 24 * 20)), sql_str(ts(0))] for p in PROJECTS.values()]
mem_rows.append([sql_str(TENANT), sql_str(PROJECTS['payments']), sql_str(uid('user:zhangsan')), "'OPERATOR'", "'ACTIVE'", sql_str(DEMO), sql_str(ts(60 * 24 * 10)), sql_str(ts(0))])
mem_rows.append([sql_str(TENANT), sql_str(PROJECTS['payments']), sql_str(uid('user:lisi')), "'VIEWER'", "'ACTIVE'", sql_str(DEMO), sql_str(ts(60 * 24 * 10)), sql_str(ts(0))])
for agent, proj in (('ops-copilot', 'infra'), ('sre-agent-01', 'payments'), ('db-analyst', 'data-platform'), ('ci-agent', 'payments'), ('cloud-bot', 'infra')):
    mem_rows.append([sql_str(TENANT), sql_str(PROJECTS[proj]), sql_str(AGENTS[agent]), "'OPERATOR'", "'ACTIVE'", sql_str(DEMO), sql_str(ts(60 * 24 * 15)), sql_str(ts(0))])
insert('project_memberships', ['tenant_id', 'project_id', 'principal_id', 'role', 'status', 'created_by', 'created_at', 'updated_at'], mem_rows)

# ---------------------------------------------------------------- connectors
conn_rows = []
conn_spec = [
    ('k8s-prod-01', 'kubernetes', 'production', 'v1.4.2', 0.05, 'payments', ['discover', 'execute'], {'cluster': 'prod-cluster-01', 'api_server': 'https://k8s-prod.acme.internal:6443'}),
    ('k8s-staging-01', 'kubernetes', 'testing', 'v1.4.2', 0.12, 'payments', ['discover', 'execute'], {'cluster': 'staging-cluster-01', 'api_server': 'https://k8s-stg.acme.internal:6443'}),
    ('linux-bastion-02', 'linux', 'production', 'v1.3.0', 0.08, 'infra', ['discover', 'execute'], {'host': 'bastion-02.acme.internal', 'os': 'Ubuntu 24.04'}),
    ('jenkins-ci', 'jenkins', 'production', 'v1.2.1', 0.18, 'payments', ['discover', 'execute'], {'url': 'http://jenkins.acme.internal:8080'}),
    ('db-proxy-01', 'database', 'production', 'v1.1.4', 0.15, 'data-platform', ['discover', 'execute'], {'engine': 'mysql', 'host': 'mysql-prod.acme.internal:3306'}),
    ('h3c-core-sw', 'network', 'production', 'v0.9.2', 10, 'infra', ['discover'], {'host': 'core-sw-01.acme.internal', 'vendor': 'h3c'}),
]
for name, ctype, env, ver, hb_min, proj, caps, cfg in conn_spec:
    conn_rows.append([sql_str(CONNECTORS[name]), sql_str(name), sql_str(ctype), "'ACTIVE'", sql_str(env), sql_str(cfg.get('host', cfg.get('cluster', cfg.get('url', '')))),
                      sql_str(ver), j(caps), j({}), j(cfg), sql_str(ts(hb_min)), sql_str(TENANT), sql_str(ts(60 * 24 * 25)), sql_str(ts(hb_min)), 'NULL', 'NULL', sql_str(PROJECTS[proj])])
insert('connectors', ['id', 'name', 'connector_type', 'status', 'environment', 'target_host', 'version', 'capabilities', 'permissions', 'config', 'last_heartbeat', 'tenant_id', 'created_at', 'updated_at', 'task_key_ciphertext', 'archived_at', 'project_id'], conn_rows)

# ---------------------------------------------------------------- integrations
insert('integrations', ['id', 'connector_id', 'protocol', 'name', 'endpoint', 'username', 'credential_ref', 'scope', 'status', 'last_probe_at', 'last_error', 'tenant_id', 'created_at', 'updated_at', 'project_id', 'environment'], [
    [sql_str(INTEGRATIONS['jenkins-acme']), sql_str(CONNECTORS['jenkins-ci']), sql_str('jenkins'), sql_str('Jenkins · Acme CI'), sql_str('http://jenkins.acme.internal:8080'), sql_str('ci-reader'), sql_str('demo:jenkins'), j({'jobs': ['build-pipeline', 'nightly-job', 'deploy-prod']}), "'ACTIVE'", sql_str(ts(2)), 'NULL', sql_str(TENANT), sql_str(ts(60 * 24 * 20)), sql_str(ts(2)), sql_str(PROJECTS['payments']), sql_str('production')],
    [sql_str(INTEGRATIONS['grafana-prod']), sql_str(CONNECTORS['k8s-prod-01']), sql_str('grafana'), sql_str('Grafana · Prod'), sql_str('http://grafana.acme.internal:3000'), sql_str('viewer'), sql_str('demo:grafana'), j({'dashboards': 12, 'datasources': 3}), "'ACTIVE'", sql_str(ts(3)), 'NULL', sql_str(TENANT), sql_str(ts(60 * 24 * 20)), sql_str(ts(3)), sql_str(PROJECTS['payments']), sql_str('production')],
    [sql_str(INTEGRATIONS['mysql-prod']), sql_str(CONNECTORS['db-proxy-01']), sql_str('mysql'), sql_str('MySQL · orders'), sql_str('mysql-prod.acme.internal:3306'), sql_str('ro_analyst'), sql_str('demo:mysql'), j({'schemas': ['orders', 'users']}), "'ACTIVE'", sql_str(ts(1)), 'NULL', sql_str(TENANT), sql_str(ts(60 * 24 * 18)), sql_str(ts(1)), sql_str(PROJECTS['data-platform']), sql_str('production')],
])

# ---------------------------------------------------------------- resources
RES = {}
res_rows = []


def resource(key, rtype, external, name, conn, ns, env, proj, meta=None, integration=None, status='ACTIVE'):
    rid = uid(f'resource:{key}')
    RES[key] = rid
    res_rows.append([sql_str(rid), sql_str(rtype), sql_str(external), sql_str(name), sql_str(ns) if ns else 'NULL',
                     sql_str(CONNECTORS[conn]), sql_str('acme-' + proj), sql_str(env), sql_str(status),
                     j({}), j({}), j(meta or {}), sql_str(TENANT), sql_str(ts(60 * 24 * 20)), sql_str(ts(2)), sql_str(ts(2)), '1',
                     sql_str(INTEGRATIONS[integration]) if integration else 'NULL', "'connector'", "'INHERITED'"])


for dep, img in (('payments-api', 'v2.14.2'), ('orders-api', 'v1.9.0'), ('gateway', 'v3.2.1'), ('auth-service', 'v2.0.4')):
    resource(f'prod/{dep}', 'deployment', f'prod/{dep}', dep, 'k8s-prod-01', 'prod', 'production', 'payments', {'image': f'registry.acme.io/{dep}:{img}', 'replicas': 3})
for pod in ('gateway-7f9c', 'payments-api-6d8f', 'auth-2b4d', 'orders-api-5b7c'):
    resource(f'prod/pod/{pod}', 'pod', f'prod/{pod}', pod, 'k8s-prod-01', 'prod', 'production', 'payments', {'node': 'node-prod-03', 'restarts': 0})
resource('prod/svc/payments-api', 'service', 'prod/payments-api', 'payments-api', 'k8s-prod-01', 'prod', 'production', 'payments', {'type': 'ClusterIP', 'ports': '8080/TCP'})
resource('prod/svc/gateway', 'service', 'prod/gateway', 'gateway', 'k8s-prod-01', 'prod', 'production', 'payments', {'type': 'LoadBalancer', 'ports': '443/TCP'})
resource('prod/ns', 'namespace', 'prod', 'prod', 'k8s-prod-01', None, 'production', 'payments', {})
for dep in ('orders-api', 'web'):
    resource(f'stg/{dep}', 'deployment', f'staging/{dep}', f'{dep} (staging)', 'k8s-staging-01', 'staging', 'testing', 'payments', {'image': f'registry.acme.io/{dep}:dev', 'replicas': 1})
resource('stg/pod/web-6d8f', 'pod', 'staging/web-6d8f', 'web-6d8f', 'k8s-staging-01', 'staging', 'testing', 'payments', {'node': 'node-stg-01', 'restarts': 1})
resource('bastion-02', 'linux_host', 'bastion-02', 'bastion-02', 'linux-bastion-02', None, 'production', 'infra', {'os': 'Ubuntu 24.04', 'kernel': '6.8.0', 'cpu': 8, 'mem_gb': 32})
for svc in ('nginx', 'redis', 'node_exporter'):
    resource(f'bastion-02/{svc}', 'systemd_service', f'bastion-02/{svc}', svc, 'linux-bastion-02', None, 'production', 'infra', {'state': 'active', 'enabled': True})
for c in ('nginx', 'redis'):
    resource(f'bastion-02/docker/{c}', 'docker_container', f'bastion-02/{c}', c, 'linux-bastion-02', None, 'production', 'infra', {'image': f'{c}:1.27', 'state': 'running'})
for job in ('build-pipeline', 'nightly-job', 'deploy-prod'):
    resource(f'jenkins/{job}', 'jenkins_job', job, job, 'jenkins-ci', None, 'production', 'payments', {'last_build': '#482', 'status': 'SUCCESS'}, integration='jenkins-acme')
resource('mysql-prod', 'database_instance', 'mysql-prod', 'mysql-prod', 'db-proxy-01', None, 'production', 'data-platform', {'engine': 'mysql 8.0', 'size_gb': 240}, integration='mysql-prod')
resource('mysql-prod/orders', 'database_schema', 'mysql-prod/orders', 'orders', 'db-proxy-01', None, 'production', 'data-platform', {'tables': 14}, integration='mysql-prod')
for tbl, rows_n in (('orders', 1840000), ('users', 92000), ('payments', 760000)):
    resource(f'mysql-prod/orders/{tbl}', 'database_table', f'mysql-prod/orders/{tbl}', tbl, 'db-proxy-01', 'orders', 'production', 'data-platform', {'rows': rows_n, 'engine': 'InnoDB'}, integration='mysql-prod')
resource('core-sw-01', 'network_device', 'core-sw-01', 'core-sw-01', 'h3c-core-sw', None, 'production', 'infra', {'vendor': 'H3C', 'model': 'S6520X', 'ports': 48}, status='DISABLED')
resource('core-sw-01/GE1-0-1', 'network_interface', 'core-sw-01/GE1/0/1', 'GE1/0/1', 'h3c-core-sw', None, 'production', 'infra', {'speed': '10G', 'state': 'up'}, status='DISABLED')
resource('grafana-prod', 'grafana_instance', 'grafana-prod', 'Grafana Prod', 'k8s-prod-01', None, 'production', 'payments', {'version': '11.2'}, integration='grafana-prod')
insert('resources', ['id', 'resource_type', 'external_id', 'name', 'namespace', 'connector_id', 'owner_team', 'environment', 'status', 'labels', 'annotations', 'metadata', 'tenant_id', 'created_at', 'updated_at', 'last_seen_at', 'sync_generation', 'integration_id', 'management_type', 'project_scope_mode'], res_rows)

# resource <-> action catalog links
def action_id(name):
    return f"(SELECT id FROM action_definitions WHERE name={sql_str(name)} ORDER BY version DESC LIMIT 1)"


ra_rows = []
links = {
    'deployment': ['deployment.update_image', 'deployment.get', 'deployment.restart', 'deployment.scale', 'deployment.rollout_status'],
    'pod': ['pod.logs.read', 'pod.get', 'pod.events', 'pod.metrics'],
    'service': ['service.get'],
    'namespace': ['namespace.list'],
    'linux_host': ['linux.host.info'],
    'systemd_service': ['systemd.service.restart'],
    'docker_container': ['docker.container.inspect', 'docker.container.logs', 'docker.container.restart'],
    'jenkins_job': ['jenkins.job.build'],
    'database_instance': ['database.instance.get', 'database.schema.list'],
    'database_schema': ['database.schema.get', 'database.table.list'],
    'database_table': ['database.query.execute', 'database.query.preview', 'database.table.describe'],
    'network_device': [],
    'network_interface': [],
    'grafana_instance': ['grafana.instance.get', 'grafana.dashboard.list'],
}
for key, rid in RES.items():
    rtype = next(r[1] for r in res_rows if r[0] == sql_str(rid))
    for act in links.get(rtype.strip("'"), []):
        ra_rows.append([sql_str(uid(f'ra:{key}:{act}')), sql_str(rid), action_id(act), 'true', sql_str(ts(60 * 24 * 20))])
insert('resource_actions', ['id', 'resource_id', 'action_id', 'enabled', 'created_at'], ra_rows)

# resource <-> project bindings
rp_rows = []
for key, rid in RES.items():
    row = next(r for r in res_rows if r[0] == sql_str(rid))
    proj = row[6].strip("'").removeprefix('acme-')  # owner_team encodes the project
    rp_rows.append([sql_str(TENANT), sql_str(rid), sql_str(PROJECTS[proj]), sql_str('USE'), sql_str('MANUAL'), sql_str(DEMO), sql_str(ts(60 * 24 * 20)), sql_str(ts(0))])
insert('resource_project_bindings', ['tenant_id', 'resource_id', 'project_id', 'permission_set', 'source', 'created_by', 'created_at', 'updated_at'], rp_rows)

# ---------------------------------------------------------------- policy rules
insert('policy_matrix_rules', ['id', 'name', 'project_id', 'resource_type', 'action_name', 'environment', 'risk_level', 'principal_kind', 'decision', 'priority', 'enabled', 'description', 'hit_count', 'last_hit_at', 'created_at', 'updated_at', 'tenant_id'], [
    [sql_str(uid('rule:low-allow')), sql_str('低风险只读自动放行'), 'NULL', sql_str('*'), sql_str('*'), sql_str('*'), sql_str('low'), 'NULL', sql_str('ALLOW'), '100', 'true', sql_str('低风险动作（读取/查询类）无需人工介入'), '39', sql_str(ts(4)), sql_str(ts(60 * 24 * 20)), sql_str(ts(0)), sql_str(TENANT)],
    [sql_str(uid('rule:prod-image')), sql_str('生产镜像更新需审批'), 'NULL', sql_str('deployment'), sql_str('deployment.update_image'), sql_str('production'), 'NULL', 'NULL', sql_str('REQUIRE_APPROVAL'), '80', 'true', sql_str('生产环境镜像变更必须人工签署'), '6', sql_str(ts(16)), sql_str(ts(60 * 24 * 20)), sql_str(ts(0)), sql_str(TENANT)],
    [sql_str(uid('rule:db-cost')), sql_str('数据库查询成本门禁'), 'NULL', sql_str('database_table'), sql_str('database.query.execute'), sql_str('*'), 'NULL', 'NULL', sql_str('REQUIRE_APPROVAL'), '80', 'true', sql_str('查询预检超过成本阈值时转人工审批'), '4', sql_str(ts(16)), sql_str(ts(60 * 24 * 18)), sql_str(ts(0)), sql_str(TENANT)],
    [sql_str(uid('rule:critical-deny')), sql_str('关键动作默认拒绝'), 'NULL', sql_str('*'), sql_str('*'), sql_str('*'), sql_str('critical'), 'NULL', sql_str('DENY'), '90', 'true', sql_str('critical 风险动作无 Grant 一律拒绝'), '2', sql_str(ts(55)), sql_str(ts(60 * 24 * 20)), sql_str(ts(0)), sql_str(TENANT)],
    [sql_str(uid('rule:staging-allow')), sql_str('测试环境放宽'), 'NULL', sql_str('*'), sql_str('*'), sql_str('testing'), 'NULL', 'NULL', sql_str('ALLOW'), '60', 'true', sql_str('staging 环境中等风险以下自动放行'), '12', sql_str(ts(130)), sql_str(ts(60 * 24 * 18)), sql_str(ts(0)), sql_str(TENANT)],
])

# ---------------------------------------------------------------- executions
def params_for(act, res_key):
    if act == 'deployment.update_image':
        return {'image': f'registry.acme.io/{res_key.split("/")[-1]}:v2.14.3', 'container': 'api'}
    if act == 'pod.logs.read':
        return {'tail_lines': 200, 'since_minutes': 30}
    if act == 'database.query.execute':
        return {'sql': 'SELECT order_id, status, latency_ms FROM orders WHERE created_at > NOW() - INTERVAL 1 HOUR LIMIT 200'}
    if act == 'systemd.service.restart':
        return {'service': res_key.split('/')[-1]}
    if act == 'jenkins.job.build':
        return {'branch': 'main'}
    return {}


exec_rows = []
audit_rows = []
plan = [
    # (minutes_ago, status, agent, action, resource_key, connector)
    (2, 'RUNNING', 'sre-agent-01', 'deployment.update_image', 'prod/payments-api', 'k8s-prod-01'),
    (4, 'RUNNING', 'ci-agent', 'jenkins.job.build', 'jenkins/build-pipeline', 'jenkins-ci'),
    (11, 'SUCCEEDED', 'ops-copilot', 'pod.logs.read', 'prod/pod/gateway-7f9c', 'k8s-prod-01'),
    (16, 'WAITING_APPROVAL', 'db-analyst', 'database.query.execute', 'mysql-prod/orders/orders', 'db-proxy-01'),
    (25, 'WAITING_APPROVAL', 'sre-agent-01', 'deployment.update_image', 'prod/orders-api', 'k8s-prod-01'),
    (38, 'SUCCEEDED', 'cloud-bot', 'linux.host.info', 'bastion-02', 'linux-bastion-02'),
    (47, 'SUCCEEDED', 'ops-copilot', 'pod.logs.read', 'prod/pod/payments-api-6d8f', 'k8s-prod-01'),
    (55, 'FAILED', 'ops-copilot', 'systemd.service.restart', 'bastion-02/nginx', 'linux-bastion-02'),
    (68, 'SUCCEEDED', 'ci-agent', 'jenkins.job.build', 'jenkins/nightly-job', 'jenkins-ci'),
    (82, 'SUCCEEDED', 'ops-copilot', 'pod.get', 'prod/pod/auth-2b4d', 'k8s-prod-01'),
    (95, 'SUCCEEDED', 'sre-agent-01', 'deployment.rollout_status', 'prod/gateway', 'k8s-prod-01'),
    (110, 'SUCCEEDED', 'db-analyst', 'database.table.describe', 'mysql-prod/orders/users', 'db-proxy-01'),
    (128, 'SUCCEEDED', 'ops-copilot', 'pod.logs.read', 'stg/pod/web-6d8f', 'k8s-staging-01'),
    (140, 'WAITING_APPROVAL', 'sre-agent-01', 'deployment.update_image', 'prod/auth-service', 'k8s-prod-01'),
    (155, 'SUCCEEDED', 'cloud-bot', 'linux.host.info', 'bastion-02', 'linux-bastion-02'),
    (170, 'SUCCEEDED', 'ci-agent', 'jenkins.job.build', 'jenkins/build-pipeline', 'jenkins-ci'),
    (185, 'FAILED', 'db-analyst', 'database.query.execute', 'mysql-prod/orders/payments', 'db-proxy-01'),
    (200, 'SUCCEEDED', 'ops-copilot', 'pod.events', 'prod/pod/gateway-7f9c', 'k8s-prod-01'),
    (215, 'SUCCEEDED', 'sre-agent-01', 'deployment.get', 'prod/payments-api', 'k8s-prod-01'),
    (230, 'SUCCEEDED', 'ops-copilot', 'service.get', 'prod/svc/gateway', 'k8s-prod-01'),
    (245, 'SUCCEEDED', 'ci-agent', 'jenkins.job.build', 'jenkins/deploy-prod', 'jenkins-ci'),
    (260, 'SUCCEEDED', 'ops-copilot', 'pod.logs.read', 'prod/pod/orders-api-5b7c', 'k8s-prod-01'),
    (275, 'SUCCEEDED', 'cloud-bot', 'docker.container.inspect', 'bastion-02/docker/redis', 'linux-bastion-02'),
    (290, 'FAILED', 'sre-agent-01', 'deployment.update_image', 'stg/orders-api', 'k8s-staging-01'),
    (305, 'SUCCEEDED', 'ops-copilot', 'pod.metrics', 'prod/pod/payments-api-6d8f', 'k8s-prod-01'),
    (320, 'SUCCEEDED', 'db-analyst', 'database.query.preview', 'mysql-prod/orders/orders', 'db-proxy-01'),
    (335, 'SUCCEEDED', 'ci-agent', 'jenkins.job.build', 'jenkins/nightly-job', 'jenkins-ci'),
    (350, 'SUCCEEDED', 'ops-copilot', 'pod.logs.read', 'prod/pod/auth-2b4d', 'k8s-prod-01'),
    (365, 'SUCCEEDED', 'sre-agent-01', 'deployment.scale', 'stg/web', 'k8s-staging-01'),
    (380, 'SUCCEEDED', 'ops-copilot', 'namespace.list', 'prod/ns', 'k8s-prod-01'),
    (395, 'SUCCEEDED', 'cloud-bot', 'linux.host.info', 'bastion-02', 'linux-bastion-02'),
    (410, 'SUCCEEDED', 'ops-copilot', 'pod.get', 'stg/pod/web-6d8f', 'k8s-staging-01'),
    (425, 'SUCCEEDED', 'ci-agent', 'jenkins.job.build', 'jenkins/build-pipeline', 'jenkins-ci'),
    (440, 'SUCCEEDED', 'db-analyst', 'database.table.describe', 'mysql-prod/orders/orders', 'db-proxy-01'),
    (455, 'SUCCEEDED', 'ops-copilot', 'pod.logs.read', 'prod/pod/gateway-7f9c', 'k8s-prod-01'),
    (470, 'SUCCEEDED', 'sre-agent-01', 'deployment.get', 'prod/orders-api', 'k8s-prod-01'),
]
pending_requests = []
# Executions initiated by the demo admin from the console. The executions API
# is self-scoped for non-platform admins, so the demo login needs its own rows
# to populate the overview wall and execution stream.
DEMO_OWNED = {0, 2, 3, 7, 10, 13, 17, 21, 25, 29, 33}
for i, (ago, status, agent, act, res_key, conn) in enumerate(plan):
    eid = uid(f'exec:{i}')
    pid = DEMO if i in DEMO_OWNED else AGENTS[agent]
    rid = RES[res_key]
    aid = action_id(act)
    params = params_for(act, res_key)
    phash = 'sha256:' + hashlib.sha256(json.dumps(params, sort_keys=True).encode()).hexdigest()[:16]
    trace = str(uuid.uuid5(NS, f'trace:{i}'))
    result = 'NULL'
    err_code = 'NULL'
    err_msg = 'NULL'
    updated = ts(ago - 0.07)
    if status == 'SUCCEEDED':
        result = j({'message': '操作完成', 'affected': 1, 'duration_ms': 1800 + i * 37})
    elif status == 'FAILED':
        err_code = sql_str('TARGET_REJECTED')
        err_msg = sql_str('目标系统拒绝操作：原生 RBAC 权限不足（kubernetes forbidden）')
        updated = ts(ago - 0.05)
    elif status in ('RUNNING', 'WAITING_APPROVAL'):
        updated = ts(ago)
    exec_rows.append([sql_str(eid), 'NULL', sql_str(pid), sql_str(rid), aid, j(params), sql_str(phash), sql_str(status), 'NULL',
                      sql_str(f'idem-demo-{i}'), sql_str(f'task-{i:04d}'), sql_str(trace), sql_str('演示数据'), '120', 'NULL',
                      sql_str(CONNECTORS[conn]), result, err_code, err_msg, sql_str(ts(ago)), sql_str(updated), 'NULL' if i in DEMO_OWNED else sql_str(KEYS[agent]),
                      'NULL', 'NULL', 'NULL', '0', 'NULL', "'connector'", sql_str(TENANT),
                      sql_str(PROJECTS['payments' if conn in ('k8s-prod-01', 'k8s-staging-01', 'jenkins-ci') else ('data-platform' if conn == 'db-proxy-01' else 'infra')]),
                      'NULL', 'NULL', 'NULL', 'NULL'])
    # audit trail per execution
    audit_rows.append([sql_str(uid(f'audit:{i}:created')), sql_str('execution.created'), sql_str(pid), sql_str('principal'), sql_str(rid), sql_str(eid), 'NULL', 'NULL', sql_str(CONNECTORS[conn]), sql_str(trace), 'NULL',
                       sql_str(('demo' if i in DEMO_OWNED else agent) + f' 请求受控动作 {act}'), j({'demo': True}), sql_str(ts(ago)), 'NULL' if i in DEMO_OWNED else sql_str(KEYS[agent]), sql_str(TENANT), 'NULL'])
    if status == 'SUCCEEDED':
        audit_rows.append([sql_str(uid(f'audit:{i}:done')), sql_str('execution.completed'), sql_str(CONNECTORS[conn]), sql_str('connector'), sql_str(rid), sql_str(eid), 'NULL', 'NULL', sql_str(CONNECTORS[conn]), sql_str(trace), 'NULL',
                           sql_str(f'动作执行成功 · {act}'), j({'duration_ms': 1800 + i * 37}), sql_str(ts(ago - 0.07)), 'NULL', sql_str(TENANT), 'NULL'])
    elif status == 'FAILED':
        audit_rows.append([sql_str(uid(f'audit:{i}:failed')), sql_str('execution.failed'), sql_str(CONNECTORS[conn]), sql_str('connector'), sql_str(rid), sql_str(eid), 'NULL', 'NULL', sql_str(CONNECTORS[conn]), sql_str(trace), 'NULL',
                           sql_str(f'动作执行失败 · {act} · TARGET_REJECTED'), j({'error_code': 'TARGET_REJECTED'}), sql_str(ts(ago - 0.05)), 'NULL', sql_str(TENANT), 'NULL'])
    if status == 'WAITING_APPROVAL':
        pending_requests.append((i, eid, pid, rid, aid, params, phash, agent, act, res_key, ago))

insert('executions', ['id', 'grant_id', 'principal_id', 'resource_id', 'action_id', 'parameters', 'parameter_hash', 'status', 'decision_id', 'idempotency_key', 'task_id', 'trace_id', 'reason', 'timeout_seconds', 'expires_at', 'connector_id', 'result', 'error_code', 'error_message', 'created_at', 'updated_at', 'credential_id', 'dispatch_token', 'dispatch_expires_at', 'dispatched_at', 'attempt', 'pending_secret', 'executor_type', 'tenant_id', 'acting_project_id', 'external_context', 'delegation_token_id', 'on_behalf_of_principal_id', 'accountable_principal_id'], exec_rows)

# ---------------------------------------------------------------- approvals
req_rows = []
preview_rows = []
expire_min = {0: 12, 1: 25, 2: 40}
for n, (i, eid, pid, rid, aid, params, phash, agent, act, res_key, ago) in enumerate(pending_requests):
    req_id = uid(f'request:{i}')
    reason = '排查订单超时，需要查询最近 1 小时慢查询记录' if act == 'database.query.execute' else f'修复 CVE-2026-1187，升级到补丁镜像（{res_key.split("/")[-1]}）'
    decision_id = uid(f'decision:{i}')
    w(f"INSERT INTO policy_decisions (id, execution_id, principal_id, resource_id, action_id, opa_input, opa_output, result, decision_seconds, created_at, tenant_id) VALUES ({sql_str(decision_id)}, {sql_str(eid)}, {sql_str(pid)}, {sql_str(rid)}, {aid}, {j({'action': act, 'environment': 'production'})}, {j({'decision': 'REQUIRE_APPROVAL', 'rule': 'demo'})}, 'REQUIRE_APPROVAL', 0.004, {sql_str(ts(ago))}, {sql_str(TENANT)}) ON CONFLICT DO NOTHING;")
    req_rows.append([sql_str(req_id), sql_str(pid), sql_str(rid), aid, j(params), sql_str(phash), sql_str(reason), "'PENDING'", sql_str(decision_id), 'NULL', 'NULL',
                     sql_str(ts(-expire_min[n])), sql_str(ts(ago)), sql_str(ts(ago)), sql_str(eid), sql_str(TENANT),
                     sql_str(PROJECTS['data-platform' if act == 'database.query.execute' else 'payments']), 'NULL', 'NULL', 'NULL', 'NULL'])
    audit_rows.append([sql_str(uid(f'audit:req:{i}')), sql_str('access_request.created'), sql_str(pid), sql_str('principal'), sql_str(rid), sql_str(eid), 'NULL', sql_str(decision_id), 'NULL', sql_str(str(uuid.uuid5(NS, f'trace:{i}'))), 'NULL',
                       sql_str(f'策略要求人工审批 · {act}'), j({'risk': 'high'}), sql_str(ts(ago)), 'NULL' if i in DEMO_OWNED else sql_str(KEYS[agent]), sql_str(TENANT), 'NULL'])
    if act == 'database.query.execute':
        preview_rows.append([sql_str(uid(f'preview:{i}')), sql_str(INTEGRATIONS['mysql-prod']), sql_str(rid), sql_str('plan:' + phash), sql_str(phash),
                             sql_str(params['sql']), '1240', '0.82', 'false', 'false', 'true', sql_str('require_approval'), j(['using_filesort']), j({}), j({}), sql_str(TENANT), sql_str(ts(ago)), sql_str(ts(-expire_min[n]))])
insert('access_requests', ['id', 'principal_id', 'resource_id', 'action_id', 'parameters', 'parameter_hash', 'reason', 'status', 'decision_id', 'approved_by', 'approved_at', 'expires_at', 'created_at', 'updated_at', 'execution_id', 'tenant_id', 'acting_project_id', 'external_context', 'delegation_token_id', 'on_behalf_of_principal_id', 'accountable_principal_id'], req_rows)
insert('database_previews', ['id', 'integration_id', 'resource_id', 'plan_hash', 'preview_hash', 'rendered_sql', 'estimated_rows', 'estimated_cost', 'full_scan', 'using_temp_table', 'using_filesort', 'verdict', 'warnings', 'governance', 'masking', 'tenant_id', 'created_at', 'expires_at'], preview_rows)

# ---------------------------------------------------------------- extra audit
extra_audit = [
    ('connector.registered', 'connector', CONNECTORS['k8s-prod-01'], '执行通道 k8s-prod-01 注册并完成配对', 60 * 24 * 25),
    ('connector.registered', 'connector', CONNECTORS['linux-bastion-02'], '执行通道 linux-bastion-02 注册并完成配对', 60 * 24 * 24),
    ('connector.quarantined', 'connector', CONNECTORS['h3c-core-sw'], '执行通道 h3c-core-sw 心跳超时，标记离线', 10),
    ('policy.rule.updated', 'user', None, '演示管理员 更新策略「生产镜像更新需审批」', 60 * 24 * 2),
    ('access_request.approved', 'user', None, '演示管理员 批准 deployment.update_image · prod/gateway', 95),
    ('grant.issued', 'user', None, '签发限时 Grant · 单资源 · 单次 · 15 分钟', 95),
    ('permission_drift.detected', 'connector', CONNECTORS['h3c-core-sw'], '检测到 core-sw-01 接口状态漂移', 60),
]
for n, (action, atype, actor, summary, ago) in enumerate(extra_audit):
    audit_rows.append([sql_str(uid(f'audit:extra:{n}')), sql_str(action), sql_str(actor) if actor else sql_str(DEMO), sql_str(atype), 'NULL', 'NULL', 'NULL', 'NULL',
                       sql_str(actor) if atype == 'connector' else 'NULL', sql_str(str(uuid.uuid5(NS, f'extra-trace:{n}'))), 'NULL', sql_str(summary), j({'demo': True}), sql_str(ts(ago)), 'NULL', sql_str(TENANT), 'NULL'])
insert('audit_events', ['id', 'action', 'actor_id', 'actor_type', 'resource_id', 'execution_id', 'grant_id', 'decision_id', 'connector_id', 'trace_id', 'target_id', 'summary', 'metadata', 'created_at', 'credential_id', 'tenant_id', 'acting_project_id'], audit_rows)



# ---------------------------------------------------------------- apply
url = urlparse(database_url())
env = dict(os.environ, PGPASSWORD=url.password or '')
psql = find_psql()
sql = '\n'.join(out)
if '--dry-run' in sys.argv:
    open('/tmp/warden-demo-seed.sql', 'w').write(sql)
    print('SQL written to /tmp/warden-demo-seed.sql')
    sys.exit(0)
proc = subprocess.run([psql, '-h', url.hostname or 'localhost', '-p', str(url.port or 5432), '-U', url.username, '-d', url.path.lstrip('/'), '-v', 'ON_ERROR_STOP=1', '-1'],
                      input=sql, text=True, capture_output=True, env=env)
if proc.returncode != 0:
    print(proc.stderr[-3000:])
    sys.exit(f'seed failed (rolled back)')
print(f'demo tenant "{TENANT}" seeded: {len(res_rows)} resources, {len(exec_rows)} executions, {len(req_rows)} pending approvals, {len(audit_rows)} audit events')
print('login: tenant=acme users=demo/zhangsan/lisi password=12345678')
