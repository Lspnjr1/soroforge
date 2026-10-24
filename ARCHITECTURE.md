# SoroForge Architecture

SoroForge is a Go CLI and HTTP service for deploying Soroban contracts. Both entry points call the same deployment service, keeping transaction and history behavior consistent.

## Package Responsibilities

- `cmd/soroforge/` defines Cobra commands, loads the application dependencies, and formats CLI output.
- `internal/config/` reads and validates `soroforge.yaml` and environment settings, including network, constructor argument, signer, and storage configuration.
- `internal/deploy/` owns deploy, upgrade, invoke, track, status, and transaction orchestration. It coordinates the interfaces below rather than binding to a database or RPC implementation.
- `internal/stellar/` implements the Stellar RPC client, signing, operation construction, and XDR helpers. `stellar.Client` is the boundary used by deployment code.
- `internal/store/` defines `store.Store` and its memory, SQLite, and Postgres implementations. Embedded migrations in `migrations/sqlite/` and `migrations/postgres/` share version numbers; tests check that both dialects produce matching schemas.
- `internal/api/` exposes authenticated HTTP routes and JSON handling. It delegates contract behavior to the deployment service.
- `internal/catalog/` registers confirmed contracts with SoroVault; this integration is best-effort.
- `internal/netcheck/` checks configured RPC endpoints and passphrases on demand. `internal/readiness/` checks the database and RPC dependencies for `/ready`.

## Deployment Lifecycle

```text
CLI or HTTP request
        |
        v
config.Load + environment validation -> deploy.Service resolves contract/network
        |
        v
stellar.Client simulates, signs, submits, and waits for transaction confirmation
        |
        v
store.Store appends confirmed deployment history and updates current contract state
        |
        v
SoroVault registration and webhooks run best-effort
```

The CLI assembles these dependencies in `cmd/soroforge/root.go`; `serve` wires the same service into `internal/api`. `internal/deploy/service.go` provides the transaction and confirmation helpers, while `internal/deploy/deploy.go` orchestrates contract deployment and records it through `record` only after confirmation. Dry runs simulate transactions but submit and record nothing. Failed submissions are kept as attempts, separate from confirmed deployment history.

## Invariants to Preserve

- Never write a deployment history row before its on-chain transaction is confirmed. Keep failures in the attempts table.
- Never log, persist, or print signing seeds or token values. Store only public signer addresses and redact credentials from URLs and errors.
- Keep SQLite and Postgres migrations aligned in version, table, and column shape; retain matching reversible up/down migrations for each dialect.
- Keep RPC SDK details inside `internal/stellar/` and lifecycle behavior inside `internal/deploy/` so CLI and API behavior remains shared.
