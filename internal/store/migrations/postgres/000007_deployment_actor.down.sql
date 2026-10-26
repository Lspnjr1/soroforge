-- Remove actor column from deployments table.
ALTER TABLE deployments
    DROP COLUMN actor;