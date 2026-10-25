package main

import (
	"os"
	"time"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newUpgradeCmd() *cobra.Command {
	var (
		dryRun         bool
		yes            bool
		notes          string
		gitCommit      string
		wasmHash       string
		requireSpec    bool
		progress       bool
		extendTTL      bool
		confirmTimeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "upgrade <alias>",
		Short: "Upgrade a tracked contract to new bytecode",
		Long: `Replace a tracked contract's WASM, keeping its address.

Soroban has no protocol-level upgrade operation. Upgrading is something a
contract does to itself, so this uploads the new bytecode and then invokes the
contract's own upgrade entrypoint — "upgrade" by default, or whatever
'upgrade_fn' names in soroforge.yaml. A contract that does not expose such a
function, or that rejects the caller, will fail here with its own error.

The current hash is read from the ledger rather than the database, so an
upgrade applied outside SoroForge is still reported accurately.

A WASM without a contractspecv0 section still upgrades, but warns that SoroVault
cannot catalogue the new interface and clients cannot be generated for it.
--require-spec turns that warning into an error.

Progress is written to stderr while waiting for confirmation, and only when
stderr is a terminal, so stdout and --json stay clean. Use --progress to force
it on in a pipe or log.

On a network marked protected: true in soroforge.yaml, a real upgrade prints
what will happen and requires you to type the network name. --yes skips the
prompt for CI; without a terminal on stdin and without --yes the command
refuses rather than hanging. --dry-run never prompts.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkConfirmTimeout(cmd.Flags().Changed("confirm-timeout"), confirmTimeout); err != nil {
				return err
			}

			a, err := newApp(cmd.Context(), appOptions{
				requireSigner:  true,
				progress:       progressFor(progress),
				confirmTimeout: confirmTimeout,
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
				networkName, network, "upgrade", args[0], yes, dryRun); err != nil {
				return err
			}

			result, err := a.service.Upgrade(cmd.Context(), deploy.UpgradeRequest{
				Alias:       args[0],
				Network:     flags.network,
				Notes:       notes,
				DryRun:      dryRun,
				GitCommit:   gitCommit,
				WasmHash:    wasmHash,
				RequireSpec: requireSpec,
				ExtendTTL:   extendTTL,
				Actor:       "cli",
			})
			if err != nil {
				return err
			}
			return printUpgradeResult(result)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"assemble and simulate without submitting or recording anything")
	cmd.Flags().BoolVar(&extendTTL, "extend-ttl", false,
		"after confirming, extend the upgraded contract's TTLs to the network's maximum")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"skip the typed confirmation on protected networks (for CI)")
	cmd.Flags().StringVar(&notes, "notes", "",
		"free-form note recorded with this upgrade")
	cmd.Flags().BoolVar(&progress, "progress", false,
		"show progress on stderr even when stderr is not a terminal")
	cmd.Flags().DurationVar(&confirmTimeout, "confirm-timeout", 0,
		"how long to wait for a transaction to confirm (default 2m; must be positive)")
	attachAliasCompletion(cmd)
	cmd.Flags().StringVar(&gitCommit, "git-commit", "", "override the detected source commit")
	cmd.Flags().StringVar(&wasmHash, "wasm-hash", "", "upgrade to an existing on-chain WASM hash instead of local file")
	cmd.Flags().BoolVar(&requireSpec, "require-spec", false,
		"fail instead of warning when the WASM exports no contractspecv0 section")

	return cmd
}
