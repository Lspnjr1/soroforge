package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// config show prints URLs as written, so an API key held in the environment
// never reaches the terminal or a CI log.
func TestConfigShowPrintsUnexpandedEnvReferences(t *testing.T) {
	const key = "provider-key-that-must-not-leak"
	t.Setenv("SF_TEST_SHOW_KEY", key)

	dir := t.TempDir()
	path := filepath.Join(dir, "soroforge.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
version: 1
default_network: mainnet
networks:
  mainnet:
    rpc_url: https://provider.example/v1/${SF_TEST_SHOW_KEY}
    passphrase: "Public Global Stellar Network ; September 2015"
    sorovault_url: https://vault.example/${SF_TEST_SHOW_KEY}
contracts:
  counter:
    wasm: ./counter.wasm
`), 0o600))

	text := runCLI(t, "config", "show", "--config", path)
	assert.Contains(t, text, "https://provider.example/v1/${SF_TEST_SHOW_KEY}")
	assert.NotContains(t, text, key)

	out := runCLI(t, "config", "show", "--config", path, "--json")
	assert.NotContains(t, out, key)

	var view resolvedConfig
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "https://provider.example/v1/${SF_TEST_SHOW_KEY}", view.Networks["mainnet"].RPCURL)
	assert.Equal(t, "https://vault.example/${SF_TEST_SHOW_KEY}", view.Networks["mainnet"].SoroVaultURL)
}
