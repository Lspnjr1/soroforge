package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file covers where a config's paths point. Relative WASM paths resolve
// against the config file's directory rather than the process working
// directory, which is what makes `soroforge deploy` behave identically from the
// repository root, a subdirectory, or a CI job started somewhere else.

const relativePathConfig = `
version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://rpc.example.org
    passphrase: "Test SDF Network ; September 2015"
contracts:
  counter:
    wasm: ./build/counter.wasm
`

func TestResolvePathDefaultsToTheWorkingDirectory(t *testing.T) {
	// With no argument and no SOROFORGE_CONFIG, the default filename in the
	// current directory is the config — the behaviour of running bare
	// `soroforge deploy` inside a project.
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(EnvConfigPath, "")

	resolved, err := resolvePath("")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, DefaultFilename), resolved)
}

func TestResolvePathReadsEnvConfigPath(t *testing.T) {
	// An empty argument is documented to fall back to $SOROFORGE_CONFIG, which
	// is how CI points at a checked-out config from an unrelated directory.
	project := t.TempDir()
	envPath := filepath.Join(project, "ci", DefaultFilename)
	t.Setenv(EnvConfigPath, envPath)

	resolved, err := resolvePath("")
	require.NoError(t, err)
	assert.Equal(t, envPath, resolved)
}

func TestResolvePathPrefersAnExplicitPathOverEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(EnvConfigPath, filepath.Join(t.TempDir(), "elsewhere.yaml"))

	resolved, err := resolvePath(filepath.Join("nested", "custom.yaml"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "nested", "custom.yaml"), resolved)
}

func TestResolvePathKeepsAbsolutePaths(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "soroforge.yaml")

	resolved, err := resolvePath(absolute)
	require.NoError(t, err)
	assert.Equal(t, absolute, resolved)
}

func TestResolvePathReportsAnUnusableWorkingDirectory(t *testing.T) {
	// filepath.Abs needs a working directory. A container that started in a
	// directory which has since been removed is the realistic way to hit this,
	// and the failure should name the path it could not resolve.
	if runtime.GOOS == "windows" {
		// Windows refuses to delete a directory that is some process's
		// working directory, so this state cannot be produced there.
		t.Skip("Windows cannot remove the current working directory")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.RemoveAll(dir))
	if _, err := os.Getwd(); err == nil {
		t.Skip("platform still reports a working directory after it was removed")
	}

	_, err := resolvePath("soroforge.yaml")
	assert.ErrorContains(t, err, "resolve config path")
	assert.ErrorContains(t, err, "soroforge.yaml")
}

func TestWasmPathResolvesRelativeToTheConfigFileFromAnotherWorkingDirectory(t *testing.T) {
	// The config is loaded through a path relative to a directory that is not
	// the one the process ends up in. The artifact must still resolve next to
	// the config, not next to the shell.
	project := t.TempDir()
	configDir := filepath.Join(project, "services", "counter")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, DefaultFilename), []byte(relativePathConfig), 0o600))

	t.Chdir(project)

	cfg, err := Load(filepath.Join("services", "counter", DefaultFilename))
	require.NoError(t, err)

	contract, err := cfg.Contract("counter")
	require.NoError(t, err)

	want := filepath.Join(configDir, "build", "counter.wasm")
	assert.Equal(t, want, cfg.WasmPath(contract))
	assert.Equal(t, configDir, cfg.Dir())

	// Moving afterwards must not move the artifact: resolution happened when
	// the config was loaded, not when the path is asked for.
	t.Chdir(t.TempDir())
	assert.Equal(t, want, cfg.WasmPath(contract))
}

func TestWasmPathKeepsAbsoluteArtifactsUnchanged(t *testing.T) {
	// An absolute wasm path is taken as written, even though it points outside
	// the project. That is how a CI job deploys an artifact built elsewhere.
	absolute := filepath.Join(t.TempDir(), "artifacts", "counter.wasm")
	cfg, err := Load(writeConfig(t, fmt.Sprintf(`
version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://rpc.example.org
    passphrase: "Test SDF Network ; September 2015"
contracts:
  counter:
    wasm: '%s'
`, absolute)))
	require.NoError(t, err)

	contract, err := cfg.Contract("counter")
	require.NoError(t, err)
	assert.Equal(t, absolute, cfg.WasmPath(contract))
}

func TestLoadAlwaysRecordsAPathSoDirNeverFallsBack(t *testing.T) {
	// Dir's "." fallback exists for a Config built by hand. Load resolves every
	// path to an absolute one, so a loaded config always knows its directory —
	// which is what allows WasmPath to be independent of the shell.
	cfg, err := Load(writeConfig(t, relativePathConfig))
	require.NoError(t, err)

	assert.NotEmpty(t, cfg.Path())
	assert.Equal(t, filepath.Dir(cfg.Path()), cfg.Dir())
}

func TestDirOfConfigBuiltWithoutAPathIsTheWorkingDirectory(t *testing.T) {
	// A Config with no path cannot come out of Load, so this is the only way to
	// reach Dir's fallback. "." keeps the pre-existing meaning: resolve
	// relative to the process, as an embedder constructing a Config expects.
	cfg := &Config{}

	assert.Empty(t, cfg.Path())
	assert.Equal(t, ".", cfg.Dir())
	assert.Equal(t,
		filepath.Join("out", "counter.wasm"),
		cfg.WasmPath(Contract{Wasm: filepath.Join("out", "counter.wasm")}))
}
