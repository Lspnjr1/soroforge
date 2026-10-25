# Deploy troubleshooting

Common failures have distinct remedies: fund the deployer for authorization errors, verify network and passphrase for `tx_bad_auth`, rebuild oversized WASM, choose a new salt for occupied addresses, retry transient `TRY_AGAIN_LATER`, and restore archived contracts before upgrades. Run `soroforge config check` before retrying.
