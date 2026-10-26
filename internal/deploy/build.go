package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sort"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/stellar"
)

// BuildRequest selects the contracts whose configured build command to run.
//
// Building executes a command from soroforge.yaml on the local machine, so it
// is only ever triggered explicitly — `soroforge build`, or `deploy`/`upgrade`
// with --build — and is deliberately absent from the HTTP API.
type BuildRequest struct {
	// Aliases names the contracts to build. Empty means every WASM contract
	// that has a build command, top-level or its own.
	Aliases []string

	// Output receives the build command's stdout and stderr as it runs, plus
	// one line naming each command before it starts. Nil discards it.
	Output io.Writer
}

// BuildResult describes one contract's artifact after a successful build.
type BuildResult struct {
	Alias    string `json:"alias"`
	Command  string `json:"command"`
	WasmPath string `json:"wasm_path"`
	WasmHash string `json:"wasm_hash"`
	Size     int    `json:"size"`
}

// Build runs the build command configured for each requested contract in the
// config file's directory, then hashes the artifact it produced.
//
// Contracts that share a command — typically a top-level `build:` that
// compiles the whole workspace — run it once. The first failing command stops
// the run with an error and no results, so a caller that builds before
// deploying never goes on to publish a stale artifact.
//
// It needs neither a signer nor a database, so it is a package function
// rather than a Service method: `soroforge build` works in a fresh checkout.
func Build(ctx context.Context, cfg *config.Config, req BuildRequest) ([]BuildResult, error) {
	output := req.Output
	if output == nil {
		output = io.Discard
	}

	aliases, err := buildTargets(cfg, req.Aliases)
	if err != nil {
		return nil, err
	}

	ran := make(map[string]bool)
	for _, alias := range aliases {
		command := cfg.BuildCommand(cfg.Contracts[alias])
		if ran[command] {
			continue
		}
		ran[command] = true

		argv, err := config.SplitCommand(command)
		if err != nil {
			// Load validates every build command, so this only fires for a
			// Config assembled by hand.
			return nil, fmt.Errorf("build command for %s: %w", alias, err)
		}

		slog.Debug("running build", "alias", alias, "command", command, "dir", cfg.Dir())
		fmt.Fprintf(output, "==> %s: %s\n", alias, command)
		if err := runBuild(ctx, cfg.Dir(), argv, output); err != nil {
			return nil, fmt.Errorf("build for %s failed (%s): %w", alias, command, err)
		}
	}

	results := make([]BuildResult, 0, len(aliases))
	for _, alias := range aliases {
		contract := cfg.Contracts[alias]
		path := cfg.WasmPath(contract)
		wasm, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("build for %s succeeded but produced no artifact at %s; "+
				"check that its wasm path matches the build output", alias, path)
		case err != nil:
			return nil, fmt.Errorf("read wasm %s: %w", path, err)
		case len(wasm) == 0:
			return nil, fmt.Errorf("build for %s produced an empty artifact at %s", alias, path)
		}
		results = append(results, BuildResult{
			Alias:    alias,
			Command:  cfg.BuildCommand(contract),
			WasmPath: path,
			WasmHash: stellar.HashHex(stellar.WasmHash(wasm)),
			Size:     len(wasm),
		})
	}
	return results, nil
}

// buildTargets resolves the requested aliases, in sorted order. A named
// contract without a build command is an error, since the caller asked for a
// build that cannot happen; with no names, contracts without one are skipped.
func buildTargets(cfg *config.Config, requested []string) ([]string, error) {
	if len(requested) == 0 {
		var aliases []string
		for alias, contract := range cfg.Contracts {
			if cfg.BuildCommand(contract) != "" {
				aliases = append(aliases, alias)
			}
		}
		if len(aliases) == 0 {
			return nil, errors.New("no build command configured: set build: in soroforge.yaml, " +
				"at the top level or on a contract")
		}
		sort.Strings(aliases)
		return aliases, nil
	}

	seen := make(map[string]bool, len(requested))
	aliases := make([]string, 0, len(requested))
	for _, alias := range requested {
		if seen[alias] {
			continue
		}
		seen[alias] = true
		contract, err := cfg.Contract(alias)
		if err != nil {
			return nil, err
		}
		if contract.IsSAC() {
			return nil, fmt.Errorf("%s is a Stellar Asset Contract and has no WASM to build", alias)
		}
		if cfg.BuildCommand(contract) == "" {
			return nil, fmt.Errorf("no build command configured for %s: set build: in soroforge.yaml, "+
				"at the top level or under contracts.%s", alias, alias)
		}
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases, nil
}

// runBuild executes argv directly — never through a shell — in dir, streaming
// its output. Stdin is not connected: a build that prompts would otherwise
// hang a CI job.
func runBuild(ctx context.Context, dir string, argv []string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("exited with status %d", exitErr.ExitCode())
		}
		return err
	}
	return nil
}
