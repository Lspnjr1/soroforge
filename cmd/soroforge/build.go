package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [alias]",
		Short: "Run the configured build command and print the resulting WASM hash",
		Long: `Run the build command from soroforge.yaml and print the hash of the
artifact it produced — the hash a deploy or upgrade of that artifact would
install.

The command comes from the contract's own 'build:' key, or the top-level
'build:' when the contract has none. With no alias, every contract that has a
build command is built; contracts sharing a command run it once.

The command runs in the config file's directory and is executed directly, not
through a shell: quotes group arguments, but $VARIABLES, globs, pipes and &&
are not interpreted. To use a shell, say so in the config, for example
build: sh -c 'cargo build --release && wasm-opt ...'.

The build's own output is streamed to stderr, so stdout and --json carry only
the result. Builds never run implicitly: deploy and upgrade build first only
when given --build.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(flags.configPath)
			if err != nil {
				return err
			}
			results, err := deploy.Build(cmd.Context(), cfg, deploy.BuildRequest{
				Aliases: args,
				Output:  cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			return printBuildResults(results)
		},
	}
	attachAliasCompletion(cmd)
	return cmd
}

// buildBefore runs the configured build for aliases ahead of a deploy or
// upgrade, streaming its output to stderr. A failed build is returned as an
// error naming the operation, so the caller stops before anything is
// uploaded, submitted, or recorded.
func buildBefore(cmd *cobra.Command, cfg *config.Config, action string, aliases []string) error {
	results, err := deploy.Build(cmd.Context(), cfg, deploy.BuildRequest{
		Aliases: aliases,
		Output:  cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("%s aborted, nothing was submitted: %w", action, err)
	}
	for _, r := range results {
		fmt.Fprintf(cmd.ErrOrStderr(), "==> built %s: wasm hash %s\n", r.Alias, r.WasmHash)
	}
	return nil
}

func printBuildResults(results []deploy.BuildResult) error {
	if flags.jsonOutput {
		return printJSON(results)
	}
	return writeBuildResults(out(), results)
}

func writeBuildResults(w io.Writer, results []deploy.BuildResult) error {
	if len(results) == 1 {
		r := results[0]
		fmt.Fprintf(w, "Built %s.\n\n", r.Alias)
		fmt.Fprintf(w, "  WASM          %s\n", r.WasmPath)
		fmt.Fprintf(w, "  WASM hash     %s\n", r.WasmHash)
		fmt.Fprintf(w, "  Size          %d bytes\n", r.Size)
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tWASM HASH\tSIZE\tWASM")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", r.Alias, r.WasmHash, r.Size, r.WasmPath)
	}
	return tw.Flush()
}
