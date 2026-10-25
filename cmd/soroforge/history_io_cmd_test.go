package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const historyIOConfigYAML = `version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://soroban-testnet.stellar.org
    passphrase: "Test SDF Network ; September 2015"
contracts:
  counter:
    wasm: ./counter.wasm
  token:
    wasm: ./token.wasm
`

// prepareHistoryIOEnv isolates the test from ambient signing or logging
// configuration; only DATABASE_URL selects the database under test.
func prepareHistoryIOEnv(t *testing.T, databaseURL string) {
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

func newHistoryIOSQLiteURL(t *testing.T) string {
	t.Helper()
	return store.SQLiteURLPrefix + filepath.ToSlash(filepath.Join(t.TempDir(), "history.db"))
}

// migrateHistoryIODatabase applies the schema to a fresh SQLite file, as an
// operator would with `soroforge migrate up` before importing history.
func migrateHistoryIODatabase(t *testing.T, databaseURL string) {
	t.Helper()

	migrator, err := store.NewMigrator(databaseURL)
	require.NoError(t, err)
	require.NoError(t, migrator.Up())
	require.NoError(t, migrator.Close())
}

// openHistoryIOStore migrates a fresh SQLite file and opens it as a store.
func openHistoryIOStore(t *testing.T, databaseURL string) store.Store {
	t.Helper()
	ctx := context.Background()

	migrateHistoryIODatabase(t, databaseURL)

	st, err := store.Open(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	return st
}

func seedHistoryIOStore(t *testing.T, st store.Store) {
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
			Network:         "testnet",
			ContractID:      "CTOKEN00000000000000000000000000000000000000000000000000",
			CurrentWasmHash: strings.Repeat("b", 64),
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
			TxHash:         strings.Repeat("1", 64),
			Ledger:         101,
			GitCommit:      "abc123",
			Notes:          "initial deploy",
		},
		{
			Alias:          "counter",
			Network:        "testnet",
			ContractID:     "CCOUNTER000000000000000000000000000000000000000000000000",
			WasmHash:       strings.Repeat("b", 64),
			Action:         store.ActionUpgrade,
			DeployerPubkey: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			Actor:          "cli",
			TxHash:         strings.Repeat("2", 64),
			Ledger:         202,
			Notes:          "release 1.1",
		},
		{
			Alias:          "token",
			Network:        "testnet",
			ContractID:     "CTOKEN00000000000000000000000000000000000000000000000000",
			WasmHash:       strings.Repeat("c", 64),
			Action:         store.ActionDeploy,
			DeployerPubkey: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			Actor:          "ci",
			TxHash:         strings.Repeat("3", 64),
			Ledger:         303,
			Notes:          "token deploy",
		},
	} {
		_, err := st.RecordDeployment(ctx, deployment)
		require.NoError(t, err)
	}
}

// historyIOSourceFixture builds a seeded source database, a config file and an
// export written by the command itself.
func historyIOSourceFixture(t *testing.T) (configPath, sourceURL, exportPath string, source store.Store) {
	t.Helper()

	dir := t.TempDir()
	configPath = filepath.Join(dir, "soroforge.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(historyIOConfigYAML), 0o600))

	sourceURL = store.SQLiteURLPrefix + filepath.ToSlash(filepath.Join(dir, "source.db"))
	prepareHistoryIOEnv(t, sourceURL)
	source = openHistoryIOStore(t, sourceURL)
	seedHistoryIOStore(t, source)

	exportPath = filepath.Join(dir, "history-export.json")
	output := runCLI(t, "history", "export", "--out", exportPath, "--config", configPath)
	assert.Contains(t, output, "Exported 2 contract(s) and 3 deployment(s) to "+exportPath+".")

	return configPath, sourceURL, exportPath, source
}

// runHistoryIOCommandExpectError executes the CLI and returns its error while
// keeping cobra's duplicate error line out of the test log.
func runHistoryIOCommandExpectError(t *testing.T, args ...string) error {
	t.Helper()

	flags = globalFlags{}
	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func TestHistoryExportImportRoundTrip(t *testing.T) {
	configPath, _, exportPath, source := historyIOSourceFixture(t)

	raw, err := os.ReadFile(exportPath)
	require.NoError(t, err)
	exported, err := store.ParseSnapshot(raw)
	require.NoError(t, err)
	require.Len(t, exported.Contracts, 2)
	require.Len(t, exported.Deployments, 3)

	sourceSnapshot, err := store.BuildSnapshot(context.Background(), source)
	require.NoError(t, err)
	require.Equal(t, sourceSnapshot.Contracts, exported.Contracts,
		"export must preserve contract ids and timestamps")
	require.Equal(t, sourceSnapshot.Deployments, exported.Deployments,
		"export must preserve deployment ids and timestamps")

	targetURL := newHistoryIOSQLiteURL(t)
	migrateHistoryIODatabase(t, targetURL)
	prepareHistoryIOEnv(t, targetURL)

	output := runCLI(t, "history", "import", exportPath, "--config", configPath)
	assert.Contains(t, output, "Imported 2 contract(s) and 3 deployment(s) from "+exportPath+
		" (0 contract(s), 0 deployment(s) already present, skipped).")

	target := openHistoryIOStore(t, targetURL)
	targetSnapshot, err := store.BuildSnapshot(context.Background(), target)
	require.NoError(t, err)
	require.Equal(t, sourceSnapshot.Contracts, targetSnapshot.Contracts,
		"a round trip must reproduce the contracts exactly")
	require.Equal(t, sourceSnapshot.Deployments, targetSnapshot.Deployments,
		"a round trip must reproduce the deployments exactly")
}

func TestHistoryExportWithoutOutWritesJSONToStdout(t *testing.T) {
	configPath, sourceURL, _, _ := historyIOSourceFixture(t)
	prepareHistoryIOEnv(t, sourceURL)

	output := runCLI(t, "history", "export", "--config", configPath)

	require.True(t, json.Valid([]byte(output)), "stdout export must be one valid JSON document")
	snapshot, err := store.ParseSnapshot([]byte(output))
	require.NoError(t, err)
	assert.Equal(t, store.SnapshotVersion, snapshot.Version)
	assert.Len(t, snapshot.Contracts, 2)
	assert.Len(t, snapshot.Deployments, 3)
	assert.NotContains(t, output, "Exported", "the stdout form must stay pipeable JSON")
}

func TestHistoryImportRefusesNonEmptyDatabaseWithoutMerge(t *testing.T) {
	configPath, _, exportPath, _ := historyIOSourceFixture(t)
	targetURL := newHistoryIOSQLiteURL(t)
	migrateHistoryIODatabase(t, targetURL)
	prepareHistoryIOEnv(t, targetURL)

	first := runCLI(t, "history", "import", exportPath, "--config", configPath)
	require.Contains(t, first, "Imported 2 contract(s) and 3 deployment(s)")

	err := runHistoryIOCommandExpectError(t, "history", "import", exportPath, "--config", configPath)
	require.Error(t, err)
	assert.ErrorContains(t, err, "database is not empty")
	assert.ErrorContains(t, err, "--merge")

	merged := runCLI(t, "history", "import", exportPath, "--config", configPath, "--merge")
	assert.Contains(t, merged, "Imported 0 contract(s) and 0 deployment(s) from "+exportPath)
	assert.Contains(t, merged, "(2 contract(s), 3 deployment(s) already present, skipped).")
}
