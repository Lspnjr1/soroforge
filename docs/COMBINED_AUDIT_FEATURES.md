# Audit and deployment workflow features

The deployment workflow records failed deploy and upgrade attempts and exposes
them through the existing history tooling. History exports support CSV for
audits and spreadsheets. A configured `build:` command runs only when asked,
with `soroforge build` or `deploy --build` / `upgrade --build`, and never
through a shell (see the README).

These features are independent of the network target and preserve the existing
confirmation, authorization, and failure redaction rules.
