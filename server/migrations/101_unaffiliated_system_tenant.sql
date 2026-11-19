-- Seed the hidden system tenant that backs the unaffiliated sentinel slug.
-- federated_identities has a foreign key to tenants(slug), so a new SSO
-- identity that matches no email-domain rule needs a real tenant row to be
-- provisioned into the "no organization yet" state. The control plane never
-- lists this tenant and refuses to let it be created/used as a business
-- organization; it only serves as a parking state until a super admin assigns
-- the identity or a tenant invitation moves it out.
INSERT INTO tenants (slug, name, status, settings)
VALUES ('unaffiliated', '未分配组织', 'ACTIVE', jsonb_build_object('system', true))
ON CONFLICT (slug) DO NOTHING;
