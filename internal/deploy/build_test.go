package deploy_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The build tests never run a shell or a real toolchain. The "build command"
// is this test binary re-executed as a helper process (TestBuildHelperProcess),
// which does one harmless thing in its working directory and exits.

const buildHelperEnv = "SOROFORGE_TEST_BUILD_HELPER"

// TestBuildHelperProcess is not a real test: it is the program the build
// commands below execute. Arguments after "--" select what it does.
func TestBuildHelperProcess(t *testing.T) {
	if os.Getenv(buildHelperEnv) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Exit(runBuildHelper(args))
}

func runBuildHelper(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "helper: no action")
		return 2
	}
	switch args[0] {
	case "write": // write <path> <content> [count-file]
		fmt.Println("compiling", args[1])
		if len(args) > 3 {
			f, err := os.OpenFile(args[3], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return 1
			}
			_, _ = f.WriteString("x")
			_ = f.Close()
		}
		if err := os.MkdirAll(filepath.Dir(args[1]), 0o755); err != nil {
			return 1
		}
		if err := os.WriteFile(args[1], []byte(args[2]), 0o600); err != nil {
			return 1
		}
		return 0
	case "fail": // fail <message>
		fmt.Fprintln(os.Stderr, "error:", args[1])
		return 3
	case "noop":
		return 0
	}
	return 2
}

// shellQuote single-quotes s for config.SplitCommand.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// helperCommand returns a build command that runs the helper with action.
func helperCommand(t *testing.T, action ...string) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	parts := []string{shellQuote(exe), "-test.run=^TestBuildHelperProcess$", "--"}
	for _, a := range action {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// loadBuildConfig writes soroforge.yaml with the given contracts section
// (and optional top-level build) into a temp dir and loads it.
func loadBuildConfig(t *testing.T, topLevelBuild, contracts string) *config.Config {
	t.Helper()
	t.Setenv(buildHelperEnv, "1")
	body := "version: 1\ndefault_network: testnet\n"
	if topLevelBuild != "" {
		body += "build: " + yamlQuote(topLevelBuild) + "\n"
	}
	body += `networks:
  testnet:
    rpc_url: https://soroban-testnet.stellar.org
    passphrase: "Test SDF Network ; September 2015"
contracts:
` + contracts
	path := filepath.Join(t.TempDir(), config.DefaultFilename)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	cfg, err := config.Load(path)
	require.NoError(t, err)
	return cfg
}

// yamlQuote double-quotes s as a YAML scalar.
func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestBuildRunsInConfigDirAndHashesArtifact(t *testing.T) {
	cfg := loadBuildConfig(t, "", `  counter:
    wasm: ./out/counter.wasm
    build: `+yamlQuote(helperCommand(t, "write", "out/counter.wasm", "fresh bytecode"))+"\n")

	var output strings.Builder
	results, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{
		Aliases: []string{"counter"},
		Output:  &output,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	assert.Equal(t, "counter", r.Alias)
	assert.Equal(t, filepath.Join(cfg.Dir(), "out", "counter.wasm"), r.WasmPath,
		"a relative output path must land in the config file's directory")
	assert.Equal(t, sha256Hex("fresh bytecode"), r.WasmHash)
	assert.Equal(t, len("fresh bytecode"), r.Size)
	assert.Contains(t, output.String(), "compiling out/counter.wasm",
		"the build's own output must be streamed to the caller")
	assert.Contains(t, output.String(), "==> counter:", "the command is announced before it runs")
}

func TestBuildFailureReturnsErrorAndNoResults(t *testing.T) {
	cfg := loadBuildConfig(t, "", `  counter:
    wasm: ./counter.wasm
    build: `+yamlQuote(helperCommand(t, "fail", "linker exploded"))+"\n")
	// A stale artifact from an earlier build must not be reported as built.
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Dir(), "counter.wasm"), []byte("stale"), 0o600))

	var output strings.Builder
	results, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{
		Aliases: []string{"counter"},
		Output:  &output,
	})
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "build for counter failed")
	assert.Contains(t, err.Error(), "exited with status 3")
	assert.Contains(t, output.String(), "linker exploded", "stderr is streamed too")
}

func TestBuildMissingProgramIsAnError(t *testing.T) {
	cfg := loadBuildConfig(t, "", `  counter:
    wasm: ./counter.wasm
    build: soroforge-test-no-such-program-anywhere --flag
`)
	_, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{Aliases: []string{"counter"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build for counter failed")
}

func TestBuildDoesNotUseAShell(t *testing.T) {
	// If a shell were involved, $HOME would be expanded and the artifact
	// would land elsewhere; without one the helper receives it literally.
	cfg := loadBuildConfig(t, "", `  counter:
    wasm: ./counter.wasm
    build: `+yamlQuote(helperCommand(t, "write", "counter.wasm", "$HOME `id`"))+"\n")

	results, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{Aliases: []string{"counter"}})
	require.NoError(t, err)
	assert.Equal(t, sha256Hex("$HOME `id`"), results[0].WasmHash)
}

func TestBuildSucceedsButArtifactMissing(t *testing.T) {
	cfg := loadBuildConfig(t, "", `  counter:
    wasm: ./counter.wasm
    build: `+yamlQuote(helperCommand(t, "noop"))+"\n")

	_, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{Aliases: []string{"counter"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "produced no artifact at")
	assert.Contains(t, err.Error(), filepath.Join(cfg.Dir(), "counter.wasm"))
}

func TestBuildAllRunsASharedCommandOnce(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "runs")
	// The shared top-level build writes one artifact; the second contract
	// overrides it with its own command.
	shared := helperCommand(t, "write", "counter.wasm", "counter code", countFile)
	cfg := loadBuildConfig(t, shared, `  counter:
    wasm: ./counter.wasm
  also_counter:
    wasm: ./counter.wasm
  token:
    wasm: ./token.wasm
    build: `+yamlQuote(helperCommand(t, "write", "token.wasm", "token code", countFile))+`
  usdc:
    asset: native
`)

	results, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{})
	require.NoError(t, err)

	aliases := make([]string, 0, len(results))
	for _, r := range results {
		aliases = append(aliases, r.Alias)
	}
	assert.Equal(t, []string{"also_counter", "counter", "token"}, aliases,
		"every WASM contract with a build is built, in alias order; the SAC is skipped")
	assert.Equal(t, sha256Hex("counter code"), results[1].WasmHash)
	assert.Equal(t, sha256Hex("token code"), results[2].WasmHash)

	runs, err := os.ReadFile(countFile)
	require.NoError(t, err)
	assert.Equal(t, "xx", string(runs), "the shared command runs once, the override once")
}

func TestBuildWithoutCommandIsAnError(t *testing.T) {
	cfg := loadBuildConfig(t, "", `  counter:
    wasm: ./counter.wasm
  usdc:
    asset: native
`)

	_, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{Aliases: []string{"counter"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no build command configured for counter")

	_, err = deploy.Build(context.Background(), cfg, deploy.BuildRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no build command configured")

	_, err = deploy.Build(context.Background(), cfg, deploy.BuildRequest{Aliases: []string{"usdc"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Stellar Asset Contract")

	_, err = deploy.Build(context.Background(), cfg, deploy.BuildRequest{Aliases: []string{"nope"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown contract "nope"`)
}

func TestBuildStopsAtFirstFailure(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "runs")
	cfg := loadBuildConfig(t, "", `  a:
    wasm: ./a.wasm
    build: `+yamlQuote(helperCommand(t, "fail", "a is broken"))+`
  b:
    wasm: ./b.wasm
    build: `+yamlQuote(helperCommand(t, "write", "b.wasm", "b", countFile))+"\n")

	_, err := deploy.Build(context.Background(), cfg, deploy.BuildRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build for a failed")
	_, statErr := os.Stat(countFile)
	assert.True(t, os.IsNotExist(statErr), "nothing after the failing build may run")
}
