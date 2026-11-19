-- 108: 补齐外键列索引（审计追溯与级联删除性能）
-- 001 定义的外键列多数没有索引：按资源/动作/授权/决策/通道维度的
-- 审计追溯与 JOIN 随数据量退化为全表扫描。本迁移补齐三张表上
-- 实际存在且缺索引的外键列（access_grants 无 resource_id/action_id
-- 列，audit_events 无 action_id 列，均不涉及）。
-- 可空列用部分索引控制体积。幂等：IF NOT EXISTS 可重放。
-- 注意：CREATE INDEX 会短暂锁表，超大表请在低峰执行。

BEGIN;

CREATE INDEX IF NOT EXISTS idx_access_requests_resource ON access_requests(resource_id);
CREATE INDEX IF NOT EXISTS idx_access_requests_action ON access_requests(action_id);
CREATE INDEX IF NOT EXISTS idx_access_requests_decision ON access_requests(decision_id) WHERE decision_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_executions_resource ON executions(resource_id);
CREATE INDEX IF NOT EXISTS idx_executions_action ON executions(action_id);
CREATE INDEX IF NOT EXISTS idx_executions_grant ON executions(grant_id) WHERE grant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_executions_decision ON executions(decision_id) WHERE decision_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_events_resource ON audit_events(resource_id) WHERE resource_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_events_grant ON audit_events(grant_id) WHERE grant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_events_decision ON audit_events(decision_id) WHERE decision_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_events_connector ON audit_events(connector_id) WHERE connector_id IS NOT NULL;

COMMIT;