-- Reverses 000010_ttl_actions.up.sql. Narrowing the constraint re-validates
-- existing rows, so this fails loudly if any 'extend' or 'restore' rows are
-- present rather than deleting history to make itself work.
ALTER TABLE deployments
    DROP CONSTRAINT deployments_action_check,
    ADD CONSTRAINT deployments_action_check CHECK (action IN ('deploy', 'upgrade', 'upload', 'invoke', 'track'));
