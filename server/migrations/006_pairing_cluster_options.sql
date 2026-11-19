ALTER TABLE cluster_pairings
    ADD COLUMN IF NOT EXISTS available_namespaces JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS suggested_image_prefix VARCHAR(512) NOT NULL DEFAULT '';
