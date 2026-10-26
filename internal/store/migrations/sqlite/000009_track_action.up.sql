-- Add 'track' to the allowed actions in deployments table.
-- SQLite doesn't support DROP CONSTRAINT, so we recreate the table.
CREATE TABLE deployments_new (
    id              INTEGER     PRIMARY KEY AUTOINCREMENT,
    alias           TEXT        NOT NULL,
    network         TEXT        NOT NULL,
    contract_id     TEXT        NOT NULL,
    wasm_hash       TEXT        NOT NULL,
    action          TEXT        NOT NULL,
    deployer_pubkey TEXT        NOT NULL,
    actor           TEXT        NOT NULL DEFAULT 'cli',
    tx_hash         TEXT,
    ledger          INTEGER,
    git_commit      TEXT,
    dirty           INTEGER     DEFAULT 0,
    fee_charged     INTEGER,
    notes           TEXT,
    ci_run_url      TEXT,
    fn_name         TEXT,
    fn_args         TEXT,
    constructor_args TEXT,
    created_at      TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    CONSTRAINT deployments_action_check CHECK (action IN ('deploy', 'upgrade', 'upload', 'invoke', 'track'))
);

INSERT INTO deployments_new (id, alias, network, contract_id, wasm_hash, action, deployer_pubkey, actor, tx_hash, ledger, git_commit, dirty, fee_charged, notes, ci_run_url, fn_name, fn_args, constructor_args, created_at)
SELECT id, alias, network, contract_id, wasm_hash, action, deployer_pubkey, actor, tx_hash, ledger, git_commit, dirty, fee_charged, notes, ci_run_url, fn_name, fn_args, constructor_args, created_at
FROM deployments;

DROP TABLE deployments;
ALTER TABLE deployments_new RENAME TO deployments;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS deployments_network_contract_id_idx ON deployments (network, contract_id);
CREATE INDEX IF NOT EXISTS deployments_network_alias_created_at_idx ON deployments (network, alias, created_at DESC);

-- Also update deployment_attempts table if it exists
CREATE TABLE deployment_attempts_new (
    id              INTEGER     PRIMARY KEY AUTOINCREMENT,
    alias           TEXT        NOT NULL,
    network         TEXT        NOT NULL,
    action          TEXT        NOT NULL,
    stage           TEXT        NOT NULL,
    error           TEXT        NOT NULL,
    tx_hash         TEXT,
    created_at      TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    CONSTRAINT deployment_attempts_action_check CHECK (action IN ('deploy', 'upgrade', 'upload', 'invoke', 'track')),
    CONSTRAINT deployment_attempts_stage_check CHECK (stage IN ('simulate', 'submit', 'confirm'))
);

INSERT INTO deployment_attempts_new (id, alias, network, action, stage, error, tx_hash, created_at)
SELECT id, alias, network, action, stage, error, tx_hash, created_at
FROM deployment_attempts;

DROP TABLE deployment_attempts;
ALTER TABLE deployment_attempts_new RENAME TO deployment_attempts;

CREATE INDEX IF NOT EXISTS deployment_attempts_network_alias_created_at_idx ON deployment_attempts (network, alias, created_at DESC);