-- Add actor column to deployments table to record which API token or CLI
-- triggered the deployment.
-- SQLite requires adding the column with a default, then dropping the default.
ALTER TABLE deployments
    ADD COLUMN actor TEXT NOT NULL DEFAULT 'cli';