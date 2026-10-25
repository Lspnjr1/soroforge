package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin buildSigner's key-source precedence: the environment is read
// through config.Env, and config.Env.SigningSeed decides which source wins.
// They use t.Setenv and therefore cannot run in parallel.

// envConfig loads the current environment as buildSigner sees it.
func envConfig() *config.Config {
	return &config.Config{Env: config.LoadEnv()}
}

// writeKeystore writes a seed to a 0600 file — the mode the permission check
// requires — and returns its path.
func writeKeystore(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployer.key")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func TestBuildSignerSecretKeyBeatsKeystore(t *testing.T) {
	envSeed := keypair.MustRandom()
	keystoreSeed := keypair.MustRandom()
	path := writeKeystore(t, keystoreSeed.Seed())

	t.Setenv(config.EnvSecretKey, envSeed.Seed())
	t.Setenv(config.EnvKeystorePath, path)

	signer, err := buildSigner(envConfig(), true)
	require.NoError(t, err)
	require.NotNil(t, signer)

	// With both sources set, the environment variable's key must sign — a CI
	// override has to win over a developer's on-disk key.
	assert.Equal(t, envSeed.Address(), signer.Address())
	assert.NotEqual(t, keystoreSeed.Address(), signer.Address())
}

func TestBuildSignerKeystoreOnlyToleratesWhitespace(t *testing.T) {
	fileSeed := keypair.MustRandom()
	// `echo S... > keystore` leaves a trailing newline; nothing should care.
	path := writeKeystore(t, "  \n\t"+fileSeed.Seed()+"\n\n")

	t.Setenv(config.EnvSecretKey, "")
	t.Setenv(config.EnvKeystorePath, path)

	signer, err := buildSigner(envConfig(), true)
	require.NoError(t, err)
	require.NotNil(t, signer)
	assert.Equal(t, fileSeed.Address(), signer.Address())
}

func TestBuildSignerWithoutAnyKey(t *testing.T) {
	t.Setenv(config.EnvSecretKey, "")
	t.Setenv(config.EnvKeystorePath, "")

	// Read-only commands (list, history, status) run with required=false and
	// must get a nil signer rather than an error.
	signer, err := buildSigner(envConfig(), false)
	require.NoError(t, err)
	assert.Nil(t, signer)

	// Commands that submit transactions require a key and must say so.
	_, err = buildSigner(envConfig(), true)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no signing key configured")
}

func TestBuildSignerMalformedSeedErrorNeverEchoesTheSeed(t *testing.T) {
	const malformed = "SNOTASTELLARSEEDJUSTGIBBERISH12345"
	t.Setenv(config.EnvSecretKey, malformed)
	t.Setenv(config.EnvKeystorePath, "")

	_, err := buildSigner(envConfig(), true)
	require.Error(t, err)
	// A malformed seed is still secret: errors end up in logs and CI output.
	assert.NotContains(t, err.Error(), malformed)
	assert.ErrorContains(t, err, "key source")
}
