-- Align action parameter_schema with the hardcoded validateResourceParameters
-- check in handler.go, which requires parameters.name (always) and
-- parameters.namespace (when the resource has a namespace). The previous
-- schemas declared {"additionalProperties":false} with no properties, telling
-- agents via list-actions that {} is the only valid body - but the control
-- plane rejects {} with RESOURCE_PARAMETER_MISMATCH. This migration declares
-- name/namespace on every read action so the catalog matches reality.
-- Action-specific optional parameters (e.g. jenkins.job.build.parameters,
-- grafana.dashboard.create.dashboard) are preserved alongside name/namespace.

-- Jenkins read actions: add name (required). Namespace is unused for nodes
-- but declared optional so the catalog does not reject agents that include it.
UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}'
WHERE name IN (
    'jenkins.controller.get','jenkins.job.get','jenkins.job.enable','jenkins.job.disable',
    'jenkins.build.get','jenkins.build.logs','jenkins.build.stop','jenkins.queue.get',
    'jenkins.queue.cancel','jenkins.node.get','jenkins.credential_reference.get'
) AND version = 1;

-- Jenkins actions that carry additional structured parameters: merge
-- name/namespace into the existing properties.
UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"description":{"type":"string","maxLength":4000},"quietPeriod":{"type":"integer","minimum":0,"maximum":3600}},"required":["name"],"additionalProperties":false,"minProperties":2}'
WHERE name = 'jenkins.job.configure' AND version = 1;

UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"parameters":{"type":"object","maxProperties":50,"additionalProperties":{"type":["string","number","integer","boolean"]}}},"required":["name"],"additionalProperties":false}'
WHERE name IN ('jenkins.job.build','jenkins.build.retry') AND version = 1;

UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"offline":{"type":"boolean"},"message":{"type":"string","maxLength":500}},"required":["name","offline"],"additionalProperties":false}'
WHERE name = 'jenkins.node.set_offline' AND version = 1;

-- Grafana read actions: name required; namespace optional (folders/dashboards
-- may carry it for grouping context).
UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}'
WHERE name IN (
    'grafana.instance.get','grafana.folder.list','grafana.folder.get','grafana.dashboard.list',
    'grafana.dashboard.get','grafana.dashboard.export','grafana.dashboard.delete','grafana.datasource.list'
) AND version = 1;

-- Grafana actions with additional structured parameters.
UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"title":{"type":"string","maxLength":255},"uid":{"type":"string","pattern":"^[a-zA-Z0-9_-]{1,40}$"}},"required":["name","title"],"additionalProperties":false}'
WHERE name = 'grafana.folder.create' AND version = 1;

UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"dashboard":{"type":"object"},"folderUid":{"type":"string","pattern":"^[a-zA-Z0-9_-]{1,40}$"},"message":{"type":"string","maxLength":500}},"required":["name","dashboard"],"additionalProperties":false}'
WHERE name = 'grafana.dashboard.create' AND version = 1;

UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"dashboard":{"type":"object"},"folderUid":{"type":"string","pattern":"^[a-zA-Z0-9_-]{1,40}$"},"message":{"type":"string","maxLength":500},"overwrite":{"type":"boolean"}},"required":["name","dashboard"],"additionalProperties":false}'
WHERE name = 'grafana.dashboard.update' AND version = 1;

UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"queries":{"type":"array","maxItems":20,"items":{"type":"object"}},"from":{"type":"string"},"to":{"type":"string"}},"required":["name","queries"],"additionalProperties":false}'
WHERE name = 'grafana.datasource.query' AND version = 1;

-- Database read actions: name required; namespace optional (schema name for
-- tables, database name for instances).
UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}'
WHERE name IN (
    'database.instance.get','database.schema.list','database.table.list','database.table.describe'
) AND version = 1;

-- Linux/Docker read actions: previously had {"properties":{}} (no required
-- fields, no additionalProperties restriction). Tighten to declare name
-- (required) and namespace (optional for containers/services scoped to a host).
UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name"],"additionalProperties":false}'
WHERE name IN (
    'linux.host.info','linux.disk.usage',
    'systemd.service.status','docker.container.list','docker.container.inspect'
) AND version = 1;

UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200,"default":50}},"required":["name"],"additionalProperties":false}'
WHERE name = 'linux.process.list' AND version = 1;

UPDATE action_definitions SET parameter_schema =
    '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"},"lines":{"type":"integer","minimum":1,"maximum":1000,"default":200}},"required":["name"],"additionalProperties":false}'
WHERE name IN ('systemd.service.logs','docker.container.logs') AND version = 1;
