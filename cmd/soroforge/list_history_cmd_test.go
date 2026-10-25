package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const listHistoryConfigYAML = `version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://soroban-testnet.stellar.org
    passphrase: "Test SDF Network ; September 2015"
  mainnet:
    rpc_url: https://soroban-mainnet.stellar.org
    passphrase: "Public Global Stellar Network ; September 2015"
contracts:
  counter:
    wasm: ./counter.wasm
  token:
    wasm: ./token.wasm
  vault:
    wasm: ./vault.wasm
`

// prepareListHistoryEnv clears the signing and logging variables an ambient
// shell may carry, so these read-only command tests cannot fail on unrelated
// configuration. Only DATABASE_URL and the temp config file matter here.
func prepareListHistoryEnv(t *testing.T, databaseURL string) {
	t.Helper()
	for _, name := range []string{
		config.EnvConfigPath,
		config.EnvSecretKey,
		config.EnvKeystorePath,
		config.EnvSignerCommand,
		config.EnvSignerPublicKey,
		config.EnvCosignerKeystores,
		config.EnvMaxContractSize,
		config.EnvLogFormat,
	} {
		t.Setenv(name, "")
	}
	t.Setenv(config.EnvDatabaseURL, databaseURL)
}

// migratedListHistoryStore creates a migrated SQLite file in a temp directory
// and returns the store plus the config path that points commands at it.
func migratedListHistoryStore(t *testing.T) (store.Store, string) {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "soroforge.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(listHistoryConfigYAML), 0o600))

	databaseURL := store.SQLiteURLPrefix +
		filepath.ToSlash(filepath.Join(dir, "history.db"))
	prepareListHistoryEnv(t, databaseURL)

	migrator, err := store.NewMigrator(databaseURL)
	require.NoError(t, err)
	require.NoError(t, migrator.Up())
	require.NoError(t, migrator.Close())

	st, err := store.Open(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	return st, configPath
}

func seedListHistoryStore(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()

	for _, contract := range []store.Contract{
		{
			Alias:           "counter",
			Network:         "testnet",
			ContractID:      "CCOUNTER000000000000000000000000000000000000000000000000",
			CurrentWasmHash: strings.Repeat("a", 64),
		},
		{
			Alias:           "token",
			Network:         "mainnet",
			ContractID:      "CTOKEN00000000000000000000000000000000000000000000000000",
			CurrentWasmHash: strings.Repeat("b", 64),
		},
		{
			Alias:           "vault",
			Network:         "testnet",
			ContractID:      "CVAULT00000000000000000000000000000000000000000000000000",
			CurrentWasmHash: strings.Repeat("c", 64),
		},
	} {
		_, err := st.UpsertContract(ctx, contract)
		require.NoError(t, err)
	}

	for _, deployment := range []store.Deployment{
		{
			Alias:          "counter",
			Network:        "testnet",
			ContractID:     "CCOUNTER000000000000000000000000000000000000000000000000",
			WasmHash:       strings.Repeat("a", 64),
			Action:         store.ActionDeploy,
			DeployerPubkey: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			Actor:          "cli",
			Notes:          "first deploy",
		},
		{
			Alias:          "counter",
			Network:        "testnet",
			ContractID:     "CCOUNTER000000000000000000000000000000000000000000000000",
			WasmHash:       strings.Repeat("b", 64),
			Action:         store.ActionUpgrade,
			DeployerPubkey: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			Actor:          "cli",
			Notes:          "second upgrade",
		},
		{
			Alias:          "counter",
			Network:        "testnet",
			ContractID:     "CCOUNTER000000000000000000000000000000000000000000000000",
			WasmHash:       strings.Repeat("c", 64),
			Action:         store.ActionInvoke,
			DeployerPubkey: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			Actor:          "cli",
			FnName:         "increment",
			Notes:          "third invoke",
		},
	} {
		_, err := st.RecordDeployment(ctx, deployment)
		require.NoError(t, err)
	}

	for _, attempt := range []store.Attempt{
		{
			Alias:   "counter",
			Network: "testnet",
			Action:  store.ActionDeploy,
			Stage:   store.AttemptSimulate,
			Error:   "simulation rejected: missing argument",
		},
		{
			Alias:   "counter",
			Network: "testnet",
			Action:  store.ActionUpgrade,
			Stage:   store.AttemptConfirm,
			Error:   "transaction failed: host error",
			TxHash:  strings.Repeat("d", 64),
		},
	} {
		_, err := st.RecordAttempt(ctx, attempt)
		require.NoError(t, err)
	}
}

func TestListCommandAgainstSQLiteFile(t *testing.T) {
	st, configPath := migratedListHistoryStore(t)
	seedListHistoryStore(t, st)

	output := runCLI(t, "list", "--config", configPath)

	assert.Contains(t, output, "NETWORK")
	assert.Contains(t, output, "ALIAS")
	for _, want := range []string{"testnet", "mainnet", "counter", "token", "vault"} {
		assert.Contains(t, output, want)
	}

	filtered := runCLI(t, "list", "--config", configPath, "--network", "testnet")
	assert.Contains(t, filtered, "counter")
	assert.Contains(t, filtered, "vault")
	assert.NotContains(t, filtered, "token", "the mainnet contract must be filtered out")
	assert.NotContains(t, filtered, "mainnet")

	var payload struct {
		Contracts []store.Contract `json:"contracts"`
	}
	jsonOutput := runCLI(t, "list", "--config", configPath, "--json")
	require.NoError(t, json.Unmarshal([]byte(jsonOutput), &payload))
	require.Len(t, payload.Contracts, 3)

	got := map[string]store.Contract{}
	for _, contract := range payload.Contracts {
		got[contract.Network+"/"+contract.Alias] = contract
	}
	require.Contains(t, got, "testnet/counter")
	assert.Equal(t, strings.Repeat("a", 64), got["testnet/counter"].CurrentWasmHash)
	require.Contains(t, got, "mainnet/token")
	assert.Equal(t, "mainnet", got["mainnet/token"].Network)
}

func TestListCommandOnEmptySQLiteFileIsFriendly(t *testing.T) {
	_, configPath := migratedListHistoryStore(t)

	output := runCLI(t, "list", "--config", configPath)

	assert.Contains(t, output, "No contracts tracked yet.")
}

func TestHistoryCommandAgainstSQLiteFile(t *testing.T) {
	st, configPath := migratedListHistoryStore(t)
	seedListHistoryStore(t, st)

	output := runCLI(t, "history", "counter", "--config", configPath)

	assert.Contains(t, output, "History for counter on testnet (newest first):")
	for _, want := range []string{"deploy", "upgrade", "invoke", "first deploy", "second upgrade"} {
		assert.Contains(t, output, want)
	}
	// printHistory renders the list as given, and the store lists newest first.
	first := strings.Index(output, "third invoke")
	second := strings.Index(output, "second upgrade")
	third := strings.Index(output, "first deploy")
	require.NotEqual(t, -1, first)
	require.NotEqual(t, -1, second)
	require.NotEqual(t, -1, third)
	assert.Less(t, first, second, "the newest record must print first")
	assert.Less(t, second, third)

	var payload struct {
		Alias       string             `json:"alias"`
		Network     string             `json:"network"`
		Deployments []store.Deployment `json:"deployments"`
	}
	jsonOutput := runCLI(t, "history", "counter", "--config", configPath, "--json")
	require.NoError(t, json.Unmarshal([]byte(jsonOutput), &payload))
	assert.Equal(t, "counter", payload.Alias)
	assert.Equal(t, "testnet", payload.Network)
	require.Len(t, payload.Deployments, 3)
	assert.Equal(t, store.ActionInvoke, payload.Deployments[0].Action)
	assert.Equal(t, store.ActionUpgrade, payload.Deployments[1].Action)
	assert.Equal(t, store.ActionDeploy, payload.Deployments[2].Action)
	for i := 1; i < len(payload.Deployments); i++ {
		assert.Greater(t, payload.Deployments[i-1].ID, payload.Deployments[i].ID,
			"history must be newest first")
	}
}

func TestHistoryFailuresCommandAgainstSQLiteFile(t *testing.T) {
	st, configPath := migratedListHistoryStore(t)
	seedListHistoryStore(t, st)

	output := runCLI(t, "history", "counter", "--config", configPath, "--failures")

	assert.Contains(t, output, "Failed attempts for counter on testnet (newest first):")
	assert.Contains(t, output, "simulate")
	assert.Contains(t, output, "simulation rejected: missing argument")
	assert.Contains(t, output, "confirm")
	assert.Contains(t, output, "transaction failed: host error")
	assert.Contains(t, output, strings.Repeat("d", 12))
	assert.Less(t, strings.Index(output, "confirm"), strings.Index(output, "simulate"),
		"the newest failed attempt must print first")
}

func TestHistoryCommandWithoutRecordsExitsSuccessfully(t *testing.T) {
	_, configPath := migratedListHistoryStore(t)

	output := runCLI(t, "history", "ghost", "--config", configPath)

	assert.Contains(t, output, "No history for ghost on testnet.")
}
