package main

import (
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newInvokeCmd() *cobra.Command {
	var (
		dryRun    bool
		notes     string
		gitCommit string
	)

	cmd := &cobra.Command{
		Use:   "invoke <alias> <fn> [args...]",
		Short: "Invoke a function on a tracked contract",
		Long: `Call a function on a tracked contract using the deployer key.

The call is simulated, signed, submitted, and recorded in the deployment history
with the function name and arguments.

Arguments use the typed syntax:
  soroforge invoke counter increment
  soroforge invoke counter add u32:5
  soroforge invoke token transfer address:GB... u64:1000

Use --dry-run to simulate without submitting or recording anything.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd.Context(), appOptions{
				requireSigner: true,
				storeOptional: dryRun,
			})
			if err != nil {
				return err
			}
			defer a.Close()

			alias := args[0]
			fn := args[1]
			callArgs := args[2:]

			result, err := a.service.Invoke(cmd.Context(), deploy.InvokeRequest{
				Alias:     alias,
				Network:   flags.network,
				Function:  fn,
				Args:      callArgs,
				Notes:     notes,
				DryRun:    dryRun,
				GitCommit: gitCommit,
				Actor:     "cli",
			})
			if err != nil {
				return err
			}
			return printInvokeResult(result)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"assemble and simulate without submitting or recording anything")
	cmd.Flags().StringVar(&notes, "notes", "",
		"free-form note recorded with this invocation")
	cmd.Flags().StringVar(&gitCommit, "git-commit", "", "override the detected source commit")

	return cmd
}
