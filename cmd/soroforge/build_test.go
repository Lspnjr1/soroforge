package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// As in internal/deploy, the build command in these tests is this test binary
// re-executed as a helper process — never a shell or a real toolchain.

const cliBuildHelperEnv = "SOROFORGE_TEST_CLI_BUILD_HELPER"

func TestCLIBuildHelperProcess(t *testing.T) {
	if os.Getenv(cliBuildHelperEnv) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	switch {
	case len(args) == 3 && args[0] == "write":
		fmt.Println("compiling", args[1])
		if err := os.WriteFile(args[1], []byte(args[2]), 0o600); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case len(args) == 2 && args[0] == "fail":
		fmt.Fprintln(os.Stderr, "error:", args[1])
		os.Exit(3)
	}
	os.Exit(2)
}

func cliHelperCommand(t *testing.T, action ...string) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	parts := []string{quote(exe), "-test.run=^TestCLIBuildHelperProcess$", "--"}
	for _, a := range action {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

// writeBuildCLIConfig writes a config whose counter contract builds with
// buildCmd. The RPC endpoint is unroutable, so any test that got as far as
// the network would fail loudly rather than reach a real one.
func writeBuildCLIConfig(t *testing.T, buildCmd string) (dir, path string) {
	t.Helper()
	for _, name := range []string{
		config.EnvConfigPath,
		config.EnvDatabaseURL,
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
	t.Setenv(cliBuildHelperEnv, "1")

	dir = t.TempDir()
	path = filepath.Join(dir, config.DefaultFilename)
	body := `version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: http://127.0.0.1:1
    passphrase: "Test SDF Network ; September 2015"
contracts:
  counter:
    wasm: ./counter.wasm
    build: "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(buildCmd) + `"
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return dir, path
}

// execCLI runs the root command and returns stdout, stderr and the error.
func execCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	saved := flags
	t.Cleanup(func() { flags = saved })
	flags = globalFlags{}

	var errBuf bytes.Buffer
	r, w, pipeErr := os.Pipe()
	require.NoError(t, pipeErr)
	previous := os.Stdout
	os.Stdout = w
	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SetErr(&errBuf)
	err = cmd.Execute()
	os.Stdout = previous
	require.NoError(t, w.Close())
	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)
	require.NoError(t, r.Close())
	return outBuf.String(), errBuf.String(), err
}

func hexSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestBuildCommandPrintsWasmHash(t *testing.T) {
	_, path := writeBuildCLIConfig(t, cliHelperCommand(t, "write", "counter.wasm", "v2 bytecode"))

	stdout, stderr, err := execCLI(t, "build", "counter", "--config", path)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Built counter.")
	assert.Contains(t, stdout, hexSHA256("v2 bytecode"))
	assert.Contains(t, stderr, "compiling counter.wasm",
		"the build's output streams to stderr, keeping stdout for the result")
	assert.NotContains(t, stdout, "compiling")
}

func TestBuildCommandJSON(t *testing.T) {
	_, path := writeBuildCLIConfig(t, cliHelperCommand(t, "write", "counter.wasm", "v2 bytecode"))

	stdout, _, err := execCLI(t, "build", "--json", "--config", path)
	require.NoError(t, err)

	var results []deploy.BuildResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &results), "stdout must be pure JSON: %q", stdout)
	require.Len(t, results, 1)
	assert.Equal(t, "counter", results[0].Alias)
	assert.Equal(t, hexSHA256("v2 bytecode"), results[0].WasmHash)
}

func TestBuildCommandFailure(t *testing.T) {
	_, path := writeBuildCLIConfig(t, cliHelperCommand(t, "fail", "does not compile"))

	_, stderr, err := execCLI(t, "build", "counter", "--config", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build for counter failed")
	assert.Contains(t, stderr, "does not compile")
}

func TestDeployBuildAbortsWhenBuildFails(t *testing.T) {
	dir, path := writeBuildCLIConfig(t, cliHelperCommand(t, "fail", "does not compile"))
	t.Setenv(config.EnvSecretKey, keypair.MustRandom().Seed())
	// A stale artifact is present: deploying it is exactly what --build
	// must prevent.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "counter.wasm"), []byte("stale"), 0o600))

	_, _, err := execCLI(t, "deploy", "counter", "--build", "--dry-run", "--config", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploy aborted, nothing was submitted")
	assert.Contains(t, err.Error(), "build for counter failed")
}

func TestUpgradeBuildAbortsWhenBuildFails(t *testing.T) {
	_, path := writeBuildCLIConfig(t, cliHelperCommand(t, "fail", "does not compile"))
	t.Setenv(config.EnvSecretKey, keypair.MustRandom().Seed())
	databaseURL := store.SQLiteURLPrefix + filepath.ToSlash(filepath.Join(t.TempDir(), "history.db"))
	migrator, err := store.NewMigrator(databaseURL)
	require.NoError(t, err)
	require.NoError(t, migrator.Up())
	require.NoError(t, migrator.Close())
	t.Setenv(config.EnvDatabaseURL, databaseURL)

	_, _, err = execCLI(t, "upgrade", "counter", "--build", "--config", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upgrade aborted, nothing was submitted")
	assert.Contains(t, err.Error(), "build for counter failed")
}

func TestUpgradeBuildConflictsWithWasmHash(t *testing.T) {
	_, path := writeBuildCLIConfig(t, cliHelperCommand(t, "fail", "must not run"))

	_, stderr, err := execCLI(t, "upgrade", "counter", "--build",
		"--wasm-hash", strings.Repeat("ab", 32), "--config", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--build and --wasm-hash are mutually exclusive")
	assert.NotContains(t, stderr, "must not run")
}

func TestDeployWithoutBuildNeverRunsTheBuild(t *testing.T) {
	dir, path := writeBuildCLIConfig(t, cliHelperCommand(t, "write", "marker", "built"))
	t.Setenv(config.EnvSecretKey, keypair.MustRandom().Seed())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "counter.wasm"), []byte("not wasm"), 0o600))

	// The deploy fails on the artifact or the unroutable RPC; what matters is
	// that the configured build was not run on the way.
	_, _, err := execCLI(t, "deploy", "counter", "--dry-run", "--config", path)
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(dir, "marker"))
	assert.True(t, os.IsNotExist(statErr), "a configured build must never run without --build")
}
