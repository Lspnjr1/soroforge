package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// touch creates an empty file, and its directories, under dir.
func touch(t *testing.T, dir, rel string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, nil, 0o644))
}

func TestDiscoverContracts(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "target/wasm32v1-none/release/counter.wasm")
	touch(t, dir, "target/wasm32v1-none/release/token.wasm")
	touch(t, dir, "target/wasm32v1-none/release/token.optimized.wasm")
	// An older toolchain's build of counter loses to the current target's.
	touch(t, dir, "target/wasm32-unknown-unknown/release/counter.wasm")
	touch(t, dir, "target/wasm32-unknown-unknown/release/legacy.wasm")
	// Debug builds and other directories are ignored.
	touch(t, dir, "target/wasm32v1-none/debug/debugonly.wasm")
	touch(t, dir, "target/wasm32v1-none/release/deps/dep.wasm")

	found, err := DiscoverContracts(dir)
	require.NoError(t, err)

	assert.Equal(t, []FoundContract{
		{Alias: "counter", Wasm: "./target/wasm32v1-none/release/counter.wasm"},
		{Alias: "legacy", Wasm: "./target/wasm32-unknown-unknown/release/legacy.wasm"},
		{Alias: "token", Wasm: "./target/wasm32v1-none/release/token.optimized.wasm"},
	}, found)
}

func TestScaffoldLoadsAndValidates(t *testing.T) {
	for name, found := range map[string][]FoundContract{
		"with contracts": {
			{Alias: "counter", Wasm: "./target/wasm32v1-none/release/counter.wasm"},
			{Alias: "odd name", Wasm: "./target/wasm32v1-none/release/odd name.wasm"},
		},
		"none found": nil,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, DefaultFilename)
			require.NoError(t, os.WriteFile(path, Scaffold(found), 0o644))

			cfg, err := Load(path)
			require.NoError(t, err, "a generated config must load as-is:\n%s", Scaffold(found))
			assert.Equal(t, "testnet", cfg.DefaultNetwork)

			// The scaffolded testnet carries a Friendbot so that `soroforge
			// fund` works out of the box on a fresh project.
			assert.Equal(t, "https://friendbot.stellar.org",
				cfg.Networks["testnet"].FriendbotURL)

			if found == nil {
				require.Contains(t, cfg.Contracts, placeholderAlias)
				return
			}
			require.Len(t, cfg.Contracts, len(found))
			// wasm paths resolve against the config file's directory.
			contract, err := cfg.Contract("odd name")
			require.NoError(t, err)
			assert.Equal(t,
				filepath.Join(dir, "target", "wasm32v1-none", "release", "odd name.wasm"),
				cfg.WasmPath(contract))
		})
	}
}

func TestWriteScaffoldRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "target/wasm32v1-none/release/counter.wasm")
	path := filepath.Join(dir, DefaultFilename)

	found, err := WriteScaffold(dir, path, false)
	require.NoError(t, err)
	require.Len(t, found, 1)

	require.NoError(t, os.WriteFile(path, []byte("hand edited"), 0o644))
	_, err = WriteScaffold(dir, path, false)
	require.ErrorContains(t, err, "--force")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hand edited", string(raw), "an existing config is never touched without --force")

	_, err = WriteScaffold(dir, path, true)
	require.NoError(t, err)
	_, err = Load(path)
	require.NoError(t, err)
}
