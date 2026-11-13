ALTER TABLE cluster_pairings
    ADD COLUMN IF NOT EXISTS available_containers TEXT[] NOT NULL DEFAULT '{}';

-- Existing Linux connectors discovered running containers before container
-- scope was configurable. Freeze those already-catalogued stable IDs into the
-- initial allowlist so an upgrade neither broadens scope nor silently removes
-- previously visible resources. Newly created containers still require a new
-- pairing/discovery grant.
UPDATE cluster_pairings pairing
SET available_containers = COALESCE((
        SELECT array_agg(DISTINCT split_part(resource.external_id, '/docker/', 2))
        FROM resources resource
        WHERE resource.connector_id = pairing.connector_id
          AND resource.resource_type = 'docker_container'
          AND resource.external_id LIKE '%/docker/%'
    ), '{}'),
    config = jsonb_set(
        COALESCE(pairing.config, '{}'::jsonb),
        '{allowedContainers}',
        to_jsonb(COALESCE((
            SELECT array_agg(DISTINCT split_part(resource.external_id, '/docker/', 2))
            FROM resources resource
            WHERE resource.connector_id = pairing.connector_id
              AND resource.resource_type = 'docker_container'
              AND resource.external_id LIKE '%/docker/%'
        ), '{}')),
        true
    )
WHERE pairing.connector_type = 'linux';

UPDATE connectors connector
SET config = jsonb_set(
    COALESCE(connector.config, '{}'::jsonb),
    '{allowedContainers}',
    to_jsonb(COALESCE((
        SELECT array_agg(DISTINCT split_part(resource.external_id, '/docker/', 2))
        FROM resources resource
        WHERE resource.connector_id = connector.id
          AND resource.resource_type = 'docker_container'
          AND resource.external_id LIKE '%/docker/%'
    ), '{}')),
    true
)
WHERE connector.connector_type = 'linux';

INSERT INTO action_definitions
    (name, version, display_name, description, resource_type, risk_level, execution_mode, parameter_schema, timeout_seconds)
VALUES
    ('systemd.service.start', 1, '启动 systemd 服务', '仅启动已发现且在 Connector 白名单内的固定 systemd 服务', 'systemd_service', 'high', 'ASYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 120),
    ('systemd.service.stop', 1, '停止 systemd 服务', '仅停止已发现且在 Connector 白名单内的固定 systemd 服务', 'systemd_service', 'high', 'ASYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 120),
    ('systemd.service.restart', 1, '重启 systemd 服务', '仅重启已发现且在 Connector 白名单内的固定 systemd 服务', 'systemd_service', 'high', 'ASYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 120),
    ('docker.container.start', 1, '启动 Docker 容器', '仅启动已发现且在 Connector 白名单内的固定 Docker 容器 ID', 'docker_container', 'high', 'ASYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 120),
    ('docker.container.stop', 1, '停止 Docker 容器', '仅停止已发现且在 Connector 白名单内的固定 Docker 容器 ID', 'docker_container', 'high', 'ASYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 120),
    ('docker.container.restart', 1, '重启 Docker 容器', '仅重启已发现且在 Connector 白名单内的固定 Docker 容器 ID', 'docker_container', 'high', 'ASYNCHRONOUS', '{"type":"object","properties":{"name":{"type":"string"},"namespace":{"type":"string"}},"required":["name","namespace"],"additionalProperties":false}', 120)
ON CONFLICT (name, version) DO UPDATE SET
    display_name=EXCLUDED.display_name, description=EXCLUDED.description,
    resource_type=EXCLUDED.resource_type, risk_level=EXCLUDED.risk_level,
    execution_mode=EXCLUDED.execution_mode, parameter_schema=EXCLUDED.parameter_schema,
    timeout_seconds=EXCLUDED.timeout_seconds;
