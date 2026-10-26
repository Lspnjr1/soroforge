package main

import (
	"fmt"
	"os"
	"time"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var (
		dryRun         bool
		yes            bool
		notes          string
		gitCommit      string
		requireSpec    bool
		all            bool
		argFlags       []string
		argsFile       string
		progress       bool
		extendTTL      bool
		confirmTimeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "deploy <alias>",
		Short: "Deploy a contract and record it",
		Long: `Upload a contract's WASM and instantiate it on the target network.

This is two transactions: the bytecode is uploaded and becomes addressable by
its hash, then a contract instance is created from that hash. Both must confirm
before anything is recorded.

Use --dry-run to assemble and simulate the transactions without submitting
them. A dry run prints the contract address a real deploy would produce, the
estimated fees, and the assembled envelopes, and writes nothing to the
database.

A WASM without a contractspecv0 section still deploys, but warns that SoroVault
cannot catalogue it and clients cannot be generated for it. --require-spec turns
that warning into an error.

Constructor arguments come from soroforge.yaml unless overridden here for one
deploy: --arg TYPE:VALUE (repeatable) replaces them, or --args-file points at
a JSON array of the same typed arguments. The two are mutually exclusive.

With --all, every contract in soroforge.yaml that is not yet tracked on the
network is deployed, in alias order; already-tracked contracts are skipped.
The first failure stops the run and the summary reports what was done before
it. --dry-run works with --all.

Progress is written to stderr while waiting for confirmation, and only when
stderr is a terminal, so stdout and --json stay clean. Use --progress to force
it on in a pipe or log.

On a network marked protected: true in soroforge.yaml, a real deploy prints
what will happen and requires you to type the network name. --yes skips the
prompt for CI; without a terminal on stdin and without --yes the command
refuses rather than hanging. --dry-run never prompts.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkConfirmTimeout(cmd.Flags().Changed("confirm-timeout"), confirmTimeout); err != nil {
				return err
			}

			// A dry run writes nothing, so it does not need a database — that
			// makes it usable as a config check in a fresh checkout.
			a, err := newApp(cmd.Context(), appOptions{
				requireSigner:  true,
				storeOptional:  dryRun,
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
			target := "every untracked contract"
			if !all {
				target = args[0]
			}
			if err := confirmProtected(cmd.ErrOrStderr(), os.Stdin, stdinIsTerminal(),
				networkName, network, "deploy", target, yes, dryRun); err != nil {
				return err
			}

			if all {
				if cmd.Flags().Changed("arg") || cmd.Flags().Changed("args-file") {
					return fmt.Errorf(
						"--arg and --args-file are per-contract and cannot be combined with --all")
				}
				results, runErr := a.service.DeployAll(cmd.Context(), deploy.DeployAllRequest{
					Network: flags.network,
					Notes:   notes,
					DryRun:  dryRun,
				})
				if results != nil {
					if err := printDeployAll(results); err != nil {
						return err
					}
				}
				return runErr
			}

			request := deploy.DeployRequest{
				Alias:       args[0],
				Network:     flags.network,
				Notes:       notes,
				DryRun:      dryRun,
				GitCommit:   gitCommit,
				RequireSpec: requireSpec,
				ExtendTTL:   extendTTL,
				Actor:       "cli",
			}

			switch {
			case cmd.Flags().Changed("arg") && cmd.Flags().Changed("args-file"):
				return fmt.Errorf("--arg and --args-file are mutually exclusive; use one or the other")
			case cmd.Flags().Changed("args-file"):
				parsed, err := config.ParseArgsFile(argsFile)
				if err != nil {
					return err
				}
				request.ConstructorArgs = parsed
			case cmd.Flags().Changed("arg"):
				parsed := make([]config.Arg, 0, len(argFlags))
				for _, raw := range argFlags {
					arg, err := config.ParseArgFlag(raw)
					if err != nil {
						return fmt.Errorf("--arg %s: %w", raw, err)
					}
					parsed = append(parsed, arg)
				}
				request.ConstructorArgs = parsed
			}

			result, err := a.service.Deploy(cmd.Context(), request)
			if err != nil {
				return err
			}
			return printDeployResult(result)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"assemble and simulate without submitting or recording anything")
	cmd.Flags().BoolVar(&extendTTL, "extend-ttl", false,
		"after confirming, extend the new contract's TTLs to the network's maximum")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"skip the typed confirmation on protected networks (for CI)")
	cmd.Flags().StringVar(&notes, "notes", "",
		"free-form note recorded with this deployment (e.g. a ticket or release tag)")
	cmd.Flags().BoolVar(&all, "all", false,
		"deploy every configured contract that is not yet tracked on the network")
	cmd.Flags().StringArrayVar(&argFlags, "arg", nil,
		"constructor argument as TYPE:VALUE (repeatable); replaces the config's constructor_args")
	cmd.Flags().StringVar(&argsFile, "args-file", "",
		"JSON file with an array of {type, value} constructor arguments; replaces the config's")
	cmd.Flags().BoolVar(&progress, "progress", false,
		"show progress on stderr even when stderr is not a terminal")
	cmd.Flags().DurationVar(&confirmTimeout, "confirm-timeout", 0,
		"how long to wait for a transaction to confirm (default 2m; must be positive)")
	attachAliasCompletion(cmd)
	cmd.Flags().StringVar(&gitCommit, "git-commit", "", "override the detected source commit")
	cmd.Flags().BoolVar(&requireSpec, "require-spec", false,
		"fail instead of warning when the WASM exports no contractspecv0 section")

	return cmd
}
