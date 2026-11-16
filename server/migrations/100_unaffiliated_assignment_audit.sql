-- Audit action for assigning an unaffiliated SSO identity to a business
-- organization (tenant). A new federated identity that matches no email-domain
-- rule is provisioned into the unaffiliated sentinel state; a super admin (or,
-- later, an accepted tenant invitation) moves principals.tenant_id off the
-- sentinel and re-binds federated_identities. This action records that move.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'account.assigned';
