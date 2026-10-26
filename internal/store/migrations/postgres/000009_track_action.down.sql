-- Revert 'track' from the allowed actions in deployments table.
ALTER TABLE deployments
    DROP CONSTRAINT deployments_action_check,
    ADD CONSTRAINT deployments_action_check CHECK (action IN ('deploy', 'upgrade', 'upload', 'invoke'));

-- Revert deployment_attempts if it has the constraint.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'deployment_attempts' AND constraint_name = 'deployment_attempts_action_check'
    ) THEN
        ALTER TABLE deployment_attempts
            DROP CONSTRAINT deployment_attempts_action_check,
            ADD CONSTRAINT deployment_attempts_action_check CHECK (action IN ('deploy', 'upgrade', 'upload', 'invoke'));
    END IF;
END $$;