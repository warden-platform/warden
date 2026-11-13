ALTER TABLE policy_versions
    ALTER COLUMN bundle_hash TYPE VARCHAR(80);

ALTER TABLE policy_decisions
    ALTER COLUMN bundle_hash TYPE VARCHAR(80);
