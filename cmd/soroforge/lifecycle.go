package main

import (
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newExtendCmd() *cobra.Command {
	var (
		ledgers uint32
		dryRun  bool
		notes   string
	)

	cmd := &cobra.Command{
		Use:   "extend <alias>",
		Short: "Renew a tracked contract's ledger-entry TTLs",
		Long: `Extend the time a contract's instance and code entries stay live.

Every Soroban ledger entry has a TTL, and an entry whose TTL lapses is archived:
it still exists, but the contract cannot be called until it is restored.
Extending pays rent for more time now, which is far cheaper than restoring
later — run this before a quiet contract's TTL runs out, not after.

The instance and code entries are extended together, to the network's maximum
by default or to --ledgers away from the current ledger. Requests above the
network's maximum are capped at it and reported as capped. Archiving is
permanent only until restored: a contract that has already lapsed must be
recovered with ` + "`soroforge restore`" + ` instead.

Use --dry-run to simulate the transaction, see its fee, and submit nothing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd.Context(), appOptions{requireSigner: true})
			if err != nil {
				return err
			}
			defer a.Close()

			result, err := a.service.Extend(cmd.Context(), deploy.ExtendRequest{
				Alias:   args[0],
				Network: flags.network,
				Ledgers: ledgers,
				Notes:   notes,
				DryRun:  dryRun,
			})
			if err != nil {
				return err
			}
			return printExtendResult(result)
		},
	}

	cmd.Flags().Uint32Var(&ledgers, "ledgers", 0,
		"keep the contract live for this many ledgers from now (default: the network's maximum)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"assemble and simulate without submitting or recording anything")
	cmd.Flags().StringVar(&notes, "notes", "",
		"free-form note recorded with this extension")

	return cmd
}

func newRestoreCmd() *cobra.Command {
	var (
		dryRun bool
		notes  string
	)

	cmd := &cobra.Command{
		Use:   "restore <alias>",
		Short: "Restore a tracked contract whose entries were archived",
		Long: `Bring a contract's archived entries back to the live ledger.

When a contract's TTL lapses, its instance and code entries are archived: they
still exist, but the contract cannot be called. Restoring rewrites those
entries and pays for a fresh TTL, making the contract callable again — with the
same address and the same bytecode.

The ledger is checked first: if every entry is live, nothing is submitted and
the command says so. If the contract was never deployed to the network at all,
restoring is not the fix and the command says that instead.

Use --dry-run to simulate the transaction and see its fee without submitting.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd.Context(), appOptions{requireSigner: true})
			if err != nil {
				return err
			}
			defer a.Close()

			result, err := a.service.Restore(cmd.Context(), deploy.RestoreRequest{
				Alias:   args[0],
				Network: flags.network,
				Notes:   notes,
				DryRun:  dryRun,
			})
			if err != nil {
				return err
			}
			return printRestoreResult(result)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"assemble and simulate without submitting or recording anything")
	cmd.Flags().StringVar(&notes, "notes", "",
		"free-form note recorded with this restore")

	return cmd
}

func newFundCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fund",
		Short: "Top up the deployer account from a test network's Friendbot",
		Long: `Create and fund the deployer account with a network's Friendbot faucet.

This only works on networks configured with a friendbot_url — test networks
have a Friendbot, mainnet does not, and the command refuses to run without
one rather than guessing at an endpoint. A network without the setting fails
with a clear error.

An account that already has its starting balance is reported as such:
Friendbot rejects repeat requests, but "already funded" is the goal either
way, not an error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd.Context(), appOptions{
				requireSigner: true,
				storeOptional: true,
			})
			if err != nil {
				return err
			}
			defer a.Close()

			result, err := a.service.Fund(cmd.Context(), deploy.FundRequest{
				Network: flags.network,
			})
			if err != nil {
				return err
			}
			return printFundResult(result)
		},
	}
}

func newWhoAmICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the deployer account and its state on a network",
		Long: `Show which account SoroForge signs as, and where its key comes from.

Prints the deployer's public address, whether the key came from the
environment or a keystore file (never the key itself), the target network, and
the account's XLM balance and sequence number. An account with no ledger entry
has never been funded on the network, and the command says so — along with the
Friendbot hint for networks that have one.

This is the first command to run in a new environment: a deploy failing with
"account not found" is otherwise indistinguishable from a network or
configuration problem.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd.Context(), appOptions{
				requireSigner: true,
				storeOptional: true,
			})
			if err != nil {
				return err
			}
			defer a.Close()

			result, err := a.service.WhoAmI(cmd.Context(), deploy.WhoAmIRequest{
				Network: flags.network,
			})
			if err != nil {
				return err
			}
			return printWhoAmIResult(result)
		},
	}
}
