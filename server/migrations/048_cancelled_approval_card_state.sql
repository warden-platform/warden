ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'access_request.cancelled';

UPDATE access_requests request
SET status='CANCELLED', updated_at=now()
FROM executions execution
WHERE request.execution_id=execution.id AND request.status='PENDING' AND execution.status='CANCELLED';

UPDATE notification_channels
SET events = array_append(events, 'access_request.cancelled'), updated_at = now()
WHERE type='dingtalk' AND NOT ('access_request.cancelled' = ANY(events));
