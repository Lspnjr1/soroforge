# Contract rollback

Rollback selects the previous successful WASM hash from deployment history and submits it through the normal upgrade path. It must refuse when no previous version exists, preserve the history entry, and require the same authorization and confirmation as a regular upgrade.
