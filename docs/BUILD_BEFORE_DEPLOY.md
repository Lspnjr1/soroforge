# Build before deploy

When a build command is configured, deployment should run it before resolving
and uploading the artifact. A failed build stops the operation before any
network transaction or history write, preventing stale WASM from being
published.
