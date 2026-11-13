-- Admin password authentication for Web UI login.
-- Only principals with kind='user' use password_hash; agent_instance/workload
-- principals keep password_hash NULL and continue to authenticate via API keys.
ALTER TABLE principals ADD COLUMN IF NOT EXISTS password_hash TEXT;
ALTER TABLE principals ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;
