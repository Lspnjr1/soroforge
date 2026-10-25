# Expected WASM hashes

Deploy configuration may pin an expected SHA-256 hash per contract. Before submission, SoroForge should compare the reviewed hash with the local artifact and refuse a mismatch, preventing an unreviewed rebuild from being deployed.
