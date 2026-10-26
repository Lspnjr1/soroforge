-- Add 'track' to the allowed actions in deployments table.
-- The action column is checked by a CHECK constraint, so we must drop and recreate it.
ALTER TABLE deployments
    DROP CONSTRAINT deployments_action_check,
    ADD CONSTRAINT deployments_action_check CHECK (action IN ('deploy', 'upgrade', 'upload', 'invoke', 'track'));

-- Also update deployment_attempts if it has the same constraint.
-- Check if deployment_attempts has the constraint (added in migration 000006).
-- We'll add it there as well for consistency.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'deployment_attempts' AND constraint_name = 'deployment_attempts_action_check'
    ) THEN
        ALTER TABLE deployment_attempts
            DROP CONSTRAINT deployment_attempts_action_check,
            ADD CONSTRAINT deployment_attempts_action_check CHECK (action IN ('deploy', 'upgrade', 'upload', 'invoke', 'track'));
    END IF;
END $$;