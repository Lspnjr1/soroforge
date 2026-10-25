package main

import (
	"fmt"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "status [alias]",
		Short: "Check whether contracts match their recorded state",
		Long: `Compare the WASM hash on-chain against the hash SoroForge recorded.

The ledger is the authority: if they disagree, the contract was changed outside
SoroForge and the command reports drift.

With --all, every contract SoroForge tracks on the network is checked —
including any whose alias has since been removed from soroforge.yaml — so a
single CI step can gate on all of them.

Exit codes:
  0  in sync (with --all: every contract)
  1  the check could not be completed
  2  drift, untracked, missing, or archived on-chain (with --all: any contract)

The non-zero exit on drift makes this usable as a CI gate.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			if all {
				result, err := a.service.StatusAll(cmd.Context(), flags.network)
				if err != nil {
					return err
				}
				if err := printNetworkStatus(result); err != nil {
					return err
				}
				if !result.InSync {
					cmd.SilenceErrors = true
					return &driftError{detail: fmt.Sprintf("contracts on %s are out of sync", result.Network)}
				}
				return nil
			}

			result, err := a.service.Status(cmd.Context(), deploy.StatusRequest{
				Alias:   args[0],
				Network: flags.network,
			})
			if err != nil {
				return err
			}
			if err := printStatus(result); err != nil {
				return err
			}

			if !result.InSync() {
				// Printed already; this only selects the exit code.
				cmd.SilenceErrors = true
				return &driftError{detail: result.Detail}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "check every tracked contract on the network")
	attachAliasCompletion(cmd)
	return cmd
}
