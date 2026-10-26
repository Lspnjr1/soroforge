package main

import (
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newUploadCmd() *cobra.Command {
	var (
		dryRun bool
		notes  string
	)

	cmd := &cobra.Command{
		Use:   "upload <alias>",
		Short: "Install a contract's WASM on-chain without deploying or upgrading",
		Long: `Upload a contract's bytecode so it is addressable by its hash.

Nothing is deployed or upgraded: no contract instance is created and no
existing contract changes. This is the first half of a governance-driven
upgrade — upload the new bytecode, put its hash in a proposal, and let a
separate party call the contract's upgrade function later. The hash is printed
so it can go straight into that proposal.

If the exact bytecode is already on-chain the upload is skipped, because
uploading is content-addressed and a second copy would be a no-op that still
costs a transaction.

Use --dry-run to assemble and simulate without submitting or recording
anything.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A dry run writes nothing, so it does not need a database.
			a, err := newApp(cmd.Context(), appOptions{
				requireSigner: true,
				storeOptional: dryRun,
			})
			if err != nil {
				return err
			}
			defer a.Close()

			result, err := a.service.Upload(cmd.Context(), deploy.UploadRequest{
				Alias:   args[0],
				Network: flags.network,
				Notes:   notes,
				DryRun:  dryRun,
				Actor:   "cli",
			})
			if err != nil {
				return err
			}
			return printUploadResult(result)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"assemble and simulate without submitting or recording anything")
	cmd.Flags().StringVar(&notes, "notes", "",
		"free-form note recorded with this upload (e.g. a ticket or release tag)")
	attachAliasCompletion(cmd)

	return cmd
}
