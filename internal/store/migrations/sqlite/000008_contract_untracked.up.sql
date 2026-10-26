-- Add untracked_at column to contracts table for soft-delete of tracked contracts.
ALTER TABLE contracts
    ADD COLUMN untracked_at TEXT;