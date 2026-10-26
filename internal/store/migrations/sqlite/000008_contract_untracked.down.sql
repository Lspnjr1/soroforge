-- Remove untracked_at column from contracts table.
ALTER TABLE contracts
    DROP COLUMN untracked_at;