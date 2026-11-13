-- Data consistency fixes surfaced by E2E testing:
-- 1. Protocol values must be lowercase ('jenkins', not 'Jenkins'): the API
--    normalizes on write, but legacy rows kept their original casing, which
--    broke exact-match protocol filtering in the UI.
-- 2. Integrations bound to archived (removed) connectors stayed ACTIVE and
--    surfaced as phantom "running" entries with no manageable connector.

UPDATE integrations SET protocol = lower(protocol), updated_at = now()
WHERE protocol <> lower(protocol);

UPDATE integrations ig SET status = 'DISABLED', updated_at = now()
FROM connectors c
WHERE c.id = ig.connector_id AND c.archived_at IS NOT NULL AND ig.status <> 'DISABLED';
