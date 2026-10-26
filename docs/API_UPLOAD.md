# API WASM upload

An HTTP upload endpoint should accept a contract alias and validated WASM bytes, authenticate with a write-scoped token, and return the installed hash. It must not deploy or alter contract history; callers can then use the returned hash in a separately approved upgrade request.
