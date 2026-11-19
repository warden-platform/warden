-- Publish approval for query templates. A DRAFT template cannot execute; it
-- must be submitted for publish, approved (human review of the immutable AST),
-- and only then transitioned to PUBLISHED. This table is the audit trail of
-- that approval, mirroring the rest of the policy/approval flow.

CREATE TABLE IF NOT EXISTS database_query_template_publish_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id     UUID NOT NULL REFERENCES database_query_templates(id) ON DELETE CASCADE,
    requested_by    VARCHAR(255) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING','APPROVED','REJECTED')),
    approved_by     VARCHAR(255),
    rejected_reason TEXT,
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at      TIMESTAMPTZ,
    tenant_id       VARCHAR(255) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_database_query_template_publish_template
    ON database_query_template_publish_requests(template_id, status);

DROP TRIGGER IF EXISTS trg_database_query_template_publish_requests_updated_at ON database_query_template_publish_requests;
CREATE TRIGGER trg_database_query_template_publish_requests_updated_at
    BEFORE UPDATE ON database_query_template_publish_requests
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
