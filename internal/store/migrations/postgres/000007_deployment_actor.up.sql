-- Add actor column to deployments table to record which API token or CLI
-- triggered the deployment.
ALTER TABLE deployments
    ADD COLUMN actor TEXT NOT NULL DEFAULT 'cli';

-- Backfill existing records with 'cli' as the actor.
UPDATE deployments SET actor = 'cli' WHERE actor IS NULL;