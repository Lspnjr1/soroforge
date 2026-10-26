-- Widens the action constraint for the ledger-lifecycle operations: `extend`
-- renews a contract's instance and code TTLs, and `restore` brings a
-- contract's archived entries back to the live ledger. Neither changes the
-- contract's bytecode, but both are on-chain changes worth recording.
ALTER TABLE deployments
    DROP CONSTRAINT deployments_action_check,
    ADD CONSTRAINT deployments_action_check CHECK (
        action IN ('deploy', 'upgrade', 'upload', 'invoke', 'track', 'extend', 'restore')
    );
