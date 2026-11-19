-- Stage 2 of the unaffiliated identity model: tenant self-service invitations.
-- A business-organization sub-administrator invites a (trusted) email; when a
-- federated SSO identity with that exact email is sitting in the unaffiliated
-- sentinel, they can accept and move into the inviting organization. The
-- invitation itself never creates a principal and never grants access on its
-- own — only an authenticated identity whose trusted email matches may accept.
CREATE TABLE IF NOT EXISTS principal_invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   VARCHAR(255) NOT NULL REFERENCES tenants(slug) ON DELETE CASCADE,
    email       VARCHAR(320) NOT NULL,
    role        VARCHAR(32)  NOT NULL DEFAULT 'user',
    status      VARCHAR(16)  NOT NULL DEFAULT 'PENDING',
    invited_by  UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ,
    CONSTRAINT principal_invitations_role_check CHECK (role IN ('user','tenant_admin')),
    CONSTRAINT principal_invitations_status_check CHECK (status IN ('PENDING','ACCEPTED','REVOKED'))
);

-- At most one live invitation per (organization, email).
CREATE UNIQUE INDEX IF NOT EXISTS principal_invitations_pending_unique
    ON principal_invitations (tenant_id, lower(email)) WHERE status = 'PENDING';
-- Fast lookup of everything a given email can currently accept.
CREATE INDEX IF NOT EXISTS principal_invitations_email_pending
    ON principal_invitations (lower(email)) WHERE status = 'PENDING';
