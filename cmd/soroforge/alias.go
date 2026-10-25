package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage tracked contract aliases",
		Long:  `Rename or stop tracking contract aliases while preserving history.`,
	}

	cmd.AddCommand(newAliasRenameCmd(), newAliasUntrackCmd())
	return cmd
}

func newAliasRenameCmd() *cobra.Command {
	var (
		yes    bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "rename --network <n> <old-alias> <new-alias>",
		Short: "Rename a tracked contract alias and all its history",
		Long: `Rename a tracked contract's alias and all its deployment history atomically.

The old alias must be currently tracked on the specified network. The new alias
must not already be tracked on that network. All deployment records, failed
attempts, and the current contract state are updated to use the new alias.

On a network marked protected: true, the command requires confirmation unless
--yes is provided or --dry-run is used.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldAlias := args[0]
			newAlias := args[1]

			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			networkName, network, err := a.cfg.Network(flags.network)
			if err != nil {
				return err
			}
			if err := confirmProtected(cmd.ErrOrStderr(), os.Stdin, stdinIsTerminal(),
				networkName, network, "rename", fmt.Sprintf("%s -> %s", oldAlias, newAlias), yes, dryRun); err != nil {
				return err
			}

			if dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(), "Would rename %s to %s on %s\n", oldAlias, newAlias, networkName)
				return nil
			}

			if err := a.store.RenameContract(cmd.Context(), networkName, oldAlias, newAlias); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Renamed %s to %s on %s\n", oldAlias, newAlias, networkName)
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false,
		"skip the typed confirmation on protected networks (for CI)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"show what would be renamed without making changes")
	attachAliasCompletion(cmd)
	return cmd
}

func newAliasUntrackCmd() *cobra.Command {
	var (
		yes    bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "untrack --network <n> <alias>",
		Short: "Stop tracking a contract while preserving its history",
		Long: `Stop tracking a contract on a network while keeping its deployment history.

The contract's history remains readable via 'history' and 'history --failures',
but the contract will no longer appear in 'list' or 'status --all', and
upgrades will require the contract to be tracked again first.

On a network marked protected: true, the command requires confirmation unless
--yes is provided or --dry-run is used.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]

			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			networkName, network, err := a.cfg.Network(flags.network)
			if err != nil {
				return err
			}
			if err := confirmProtected(cmd.ErrOrStderr(), os.Stdin, stdinIsTerminal(),
				networkName, network, "untrack", alias, yes, dryRun); err != nil {
				return err
			}

			if dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(), "Would untrack %s on %s\n", alias, networkName)
				return nil
			}

			if err := a.store.UntrackContract(cmd.Context(), networkName, alias); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Untracked %s on %s (history preserved)\n", alias, networkName)
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false,
		"skip the typed confirmation on protected networks (for CI)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"show what would be untracked without making changes")
	attachAliasCompletion(cmd)
	return cmd
}
