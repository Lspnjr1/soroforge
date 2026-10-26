-- Remove actor column from deployments table.
-- SQLite doesn't support DROP COLUMN directly before 3.35.0, but we use a
-- recent enough version. If needed, this would require table recreation.
ALTER TABLE deployments
    DROP COLUMN actor;