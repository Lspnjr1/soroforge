# Upload and invoke end-to-end coverage

Local-network end-to-end coverage should upload a deterministic WASM artifact,
assert the returned hash, and invoke a read-only contract method using that
artifact. The test must use a disposable local network and fail clearly when
the network is unavailable.
