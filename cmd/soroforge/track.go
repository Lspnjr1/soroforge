package main

import (
	"fmt"
	"os"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newTrackCmd() *cobra.Command {
	var (
		dryRun    bool
		yes       bool
		notes     string
		gitCommit string
	)

	cmd := &cobra.Command{
		Use:   "track --network <n> <alias> <contract-id>",
		Short: "Adopt a contract deployed elsewhere into SoroForge tracking",
		Long: `Read a contract's live WASM hash from the ledger and record it in SoroForge's
history so that status, upgrade, and history work for the contract.

The contract must already exist on-chain at the given contract ID. SoroForge
verifies the contract exists and reads its current WASM hash, then creates a
'track' history entry. This is intended for contracts deployed with the Stellar
CLI or other tools before SoroForge was adopted.

The alias must not already be tracked on the target network. The contract ID
must be a valid C... contract address on the target network.

On a network marked protected: true, the command requires confirmation unless
--yes is provided or --dry-run is used.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			contractID := args[1]

			a, err := newApp(cmd.Context(), appOptions{
				requireSigner: true,
			})
			if err != nil {
				return err
			}
			defer a.Close()

			networkName, network, err := a.cfg.Network(flags.network)
			if err != nil {
				return err
			}
			if err := confirmProtected(cmd.ErrOrStderr(), os.Stdin, stdinIsTerminal(),
				networkName, network, "track", alias, yes, dryRun); err != nil {
				return err
			}

			result, err := a.service.Track(cmd.Context(), deploy.TrackRequest{
				Alias:      alias,
				Network:    flags.network,
				ContractID: contractID,
				Notes:      notes,
				DryRun:     dryRun,
				GitCommit:  gitCommit,
				Actor:      "cli",
			})
			if err != nil {
				return err
			}
			return printTrackResult(result)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"verify the contract exists and show what would be tracked without recording anything")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"skip the typed confirmation on protected networks (for CI)")
	cmd.Flags().StringVar(&notes, "notes", "",
		"free-form note recorded with this track operation (e.g. a ticket or release tag)")
	cmd.Flags().StringVar(&gitCommit, "git-commit", "", "override the detected source commit")
	attachAliasCompletion(cmd)
	return cmd
}

func printTrackResult(r *deploy.TrackResult) error {
	if flags.jsonOutput {
		return printJSON(r)
	}

	w := out()
	fmt.Fprintf(w, "Tracked %s on %s\n", r.Alias, r.Network)
	fmt.Fprintf(w, "  Contract ID   %s\n", r.ContractID)
	fmt.Fprintf(w, "  WASM Hash     %s\n", r.WasmHash)
	fmt.Fprintf(w, "  Deployer      %s\n", r.Deployer)
	if r.Ledger != 0 {
		fmt.Fprintf(w, "  Ledger        %d\n", r.Ledger)
	}
	if r.ContractURL != "" {
		fmt.Fprintf(w, "  Contract URL  %s\n", r.ContractURL)
	}
	if r.Deployment != nil {
		fmt.Fprintf(w, "  Recorded      deployment #%d at %s\n", r.Deployment.ID, r.Deployment.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}
