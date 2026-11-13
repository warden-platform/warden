UPDATE resources
SET tenant_id='default', updated_at=now()
WHERE btrim(tenant_id)='';

UPDATE integrations
SET tenant_id='default', updated_at=now()
WHERE btrim(tenant_id)='';

UPDATE connectors
SET tenant_id='default', updated_at=now()
WHERE btrim(tenant_id)='';

UPDATE principals
SET tenant_id='default', updated_at=now()
WHERE btrim(tenant_id)='';
