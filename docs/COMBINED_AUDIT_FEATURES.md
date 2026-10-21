# Audit and deployment workflow features

The deployment workflow records failed deploy and upgrade attempts and exposes
them through the existing history tooling. History exports support CSV for
audits and spreadsheets. When a build command is configured, it runs before
artifact resolution and deployment so a stale WASM file cannot be published.

These features are independent of the network target and preserve the existing
confirmation, authorization, and failure redaction rules.
