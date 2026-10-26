package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeExpandConfig writes a config whose mainnet network uses rpcURL and
// sorovaultURL verbatim.
func writeExpandConfig(t *testing.T, rpcURL, sorovaultURL, passphrase string) string {
	t.Helper()
	body := `
version: 1
default_network: mainnet
networks:
  mainnet:
    rpc_url: "` + rpcURL + `"
    passphrase: "` + passphrase + `"
`
	if sorovaultURL != "" {
		body += `    sorovault_url: "` + sorovaultURL + `"
`
	}
	body += `contracts:
  counter:
    wasm: ./counter.wasm
`
	path := filepath.Join(t.TempDir(), "soroforge.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadExpandsEnvReferencesInURLs(t *testing.T) {
	t.Setenv("SF_TEST_RPC_KEY", "k3y-123")
	t.Setenv("SF_TEST_VAULT_HOST", "vault.example")

	path := writeExpandConfig(t,
		"https://provider.example/v1/${SF_TEST_RPC_KEY}",
		"https://${SF_TEST_VAULT_HOST}/registry",
		"Public Global Stellar Network ; September 2015")

	cfg, err := Load(path)
	require.NoError(t, err)

	n := cfg.Networks["mainnet"]
	assert.Equal(t, "https://provider.example/v1/k3y-123", n.RPCURL)
	assert.Equal(t, "https://vault.example/registry", n.SoroVaultURL)

	// What gets printed is the form written in the file.
	assert.Equal(t, "https://provider.example/v1/${SF_TEST_RPC_KEY}", n.DisplayRPCURL())
	assert.Equal(t, "https://${SF_TEST_VAULT_HOST}/registry", n.DisplaySoroVaultURL())
	assert.Equal(t, "dial https://provider.example/v1/${SF_TEST_RPC_KEY}: refused",
		n.RedactURLs("dial https://provider.example/v1/k3y-123: refused"))
}

func TestLoadRejectsUnsetEnvReference(t *testing.T) {
	t.Setenv("SF_TEST_UNSET_KEY", "")
	require.NoError(t, os.Unsetenv("SF_TEST_UNSET_KEY"))

	path := writeExpandConfig(t,
		"https://provider.example/v1/${SF_TEST_UNSET_KEY}", "",
		"Public Global Stellar Network ; September 2015")

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "networks.mainnet.rpc_url")
	assert.Contains(t, err.Error(), "${SF_TEST_UNSET_KEY}")
	assert.Contains(t, err.Error(), "not set")
}

func TestLoadRejectsEmptyEnvReference(t *testing.T) {
	t.Setenv("SF_TEST_EMPTY_HOST", "")

	path := writeExpandConfig(t,
		"https://soroban-testnet.stellar.org",
		"https://${SF_TEST_EMPTY_HOST}/registry",
		"Public Global Stellar Network ; September 2015")

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "networks.mainnet.sorovault_url")
	assert.Contains(t, err.Error(), "${SF_TEST_EMPTY_HOST}")
}

func TestLoadLeavesLiteralDollarAlone(t *testing.T) {
	// A bare $NAME is ambiguous in a URL and is not a reference.
	t.Setenv("SF_TEST_BARE", "should-not-appear")

	path := writeExpandConfig(t,
		"https://provider.example/v1/$SF_TEST_BARE/a$b",
		"",
		// The passphrase is never expanded, even with a braced reference.
		"Odd ${SF_TEST_BARE} passphrase")

	cfg, err := Load(path)
	require.NoError(t, err)

	n := cfg.Networks["mainnet"]
	assert.Equal(t, "https://provider.example/v1/$SF_TEST_BARE/a$b", n.RPCURL)
	assert.Equal(t, n.RPCURL, n.DisplayRPCURL())
	assert.Equal(t, "Odd ${SF_TEST_BARE} passphrase", n.Passphrase)
}

func TestLoadValidatesExpandedURLButReportsUnexpandedForm(t *testing.T) {
	// The variable holds a malformed host. Validation must catch it, and the
	// error must quote the ${...} form rather than the value.
	const secret = "bad host"
	t.Setenv("SF_TEST_SECRET_HOST", secret)

	path := writeExpandConfig(t,
		"https://${SF_TEST_SECRET_HOST}/v1", "",
		"Public Global Stellar Network ; September 2015")

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "networks.mainnet.rpc_url")
	assert.Contains(t, err.Error(), "${SF_TEST_SECRET_HOST}")
	assert.NotContains(t, err.Error(), secret)
}

func TestExpandEnvRefs(t *testing.T) {
	t.Setenv("SF_TEST_A", "alpha")
	t.Setenv("SF_TEST_B", "beta")

	got, ok, err := expandEnvRefs("${SF_TEST_A}-${SF_TEST_B}-${SF_TEST_A}")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "alpha-beta-alpha", got)

	got, ok, err = expandEnvRefs("no references, just $ and ${ }")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "no references, just $ and ${ }", got)
}
