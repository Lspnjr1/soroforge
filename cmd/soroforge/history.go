package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/soroworks/soroforge/internal/store"
	"github.com/spf13/cobra"
)

func parseSince(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	if strings.HasSuffix(raw, "d") {
		daysStr := strings.TrimSuffix(raw, "d")
		days, err := strconv.Atoi(daysStr)
		if err == nil && days > 0 {
			return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return time.Now().Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("invalid --since value %q: expected date (e.g. 2026-09-01) or duration (e.g. 30d, 24h)", raw)
}

func parsePruneAge(raw string) (time.Duration, error) {
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err == nil && days > 0 && days <= int((1<<63-1)/(24*time.Hour)) {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return duration, nil
	}
	return 0, fmt.Errorf("invalid --older-than value %q: expected a positive duration (e.g. 90d or 2160h)", raw)
}

func newHistoryCmd() *cobra.Command {
	var (
		failures bool
		limit    int
		sinceRaw string
	)

	cmd := &cobra.Command{
		Use:   "history <alias>",
		Short: "Show, export, or import a contract's deploy and upgrade history",
		Long: `Show every recorded change to one contract on one network, newest first.

Each entry records the WASM hash, who deployed it, the transaction, and the
ledger it landed in.

With --failures, show the contract's failed deploy and upgrade attempts
instead — transactions that were included and then failed, submissions the
network rejected, and pre-flight stops. Failures are recorded in their own
table and never appear in the normal history.

The subcommands move the whole history between databases:

  history export  write every contract and deployment as a versioned JSON document
  history import  load such a document into an empty (or, with --merge, existing) database`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return fmt.Errorf("invalid --limit value %d: must be positive", limit)
			}
			sinceTime, err := parseSince(sinceRaw)
			if err != nil {
				return err
			}

			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			alias := args[0]
			network, _, err := a.cfg.Network(flags.network)
			if err != nil {
				return err
			}

			if failures {
				attempts, err := a.service.Failures(cmd.Context(), network, alias)
				if err != nil {
					return err
				}
				if !sinceTime.IsZero() {
					filtered := attempts[:0]
					for _, att := range attempts {
						if att.CreatedAt.After(sinceTime) {
							filtered = append(filtered, att)
						}
					}
					attempts = filtered
				}
				if limit > 0 && len(attempts) > limit {
					attempts = attempts[:limit]
				}
				return printFailures(alias, network, attempts)
			}

			var deployments []store.Deployment
			if limit > 0 && sinceTime.IsZero() {
				deployments, err = a.service.HistoryPage(cmd.Context(), network, alias, limit, 0)
			} else {
				deployments, err = a.service.History(cmd.Context(), network, alias)
			}
			if err != nil {
				return err
			}

			if !sinceTime.IsZero() {
				filtered := deployments[:0]
				for _, d := range deployments {
					if d.CreatedAt.After(sinceTime) {
						filtered = append(filtered, d)
					}
				}
				deployments = filtered
			}
			if limit > 0 && len(deployments) > limit {
				deployments = deployments[:limit]
			}

			return printHistory(alias, network, deployments)
		},
	}

	cmd.Flags().BoolVar(&failures, "failures", false,
		"show failed deploy and upgrade attempts instead of confirmed history")
	cmd.Flags().IntVar(&limit, "limit", 0,
		"print the N newest records")
	cmd.Flags().StringVar(&sinceRaw, "since", "",
		"print records after a date (YYYY-MM-DD) or duration (e.g. 30d, 24h)")
	attachAliasCompletion(cmd)
	cmd.AddCommand(newHistoryExportCmd(), newHistoryImportCmd(), newHistoryPruneFailuresCmd())
	return cmd
}

func newHistoryPruneFailuresCmd() *cobra.Command {
	var (
		olderThan string
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "prune-failures",
		Short: "Remove failed attempts older than a duration",
		Long: `Remove old failed deployment attempts while preserving confirmed
deployment history. Use --dry-run to print the number of attempts that would
be removed without changing the database.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, err := parsePruneAge(olderThan)
			if err != nil {
				return err
			}
			cutoff := time.Now().UTC().Add(-age)
			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			var count int
			if dryRun {
				count, err = a.store.CountAttemptsBefore(cmd.Context(), cutoff)
			} else {
				count, err = a.store.PruneAttempts(cmd.Context(), cutoff)
			}
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Would prune %d failed attempt(s) older than %s.\n", count, olderThan)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Pruned %d failed attempt(s) older than %s.\n", count, olderThan)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "90d", "age threshold for failed attempts (e.g. 90d or 2160h)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "count matching failed attempts without deleting them")
	return cmd
}

func newHistoryExportCmd() *cobra.Command {
	var outPath string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the full deployment history as versioned JSON",
		Long: `Export every tracked contract and every recorded deploy, upgrade, and
upload to a portable JSON document.

The file is version 1 of a documented format (see internal/store/history.go
and the README): it carries the original ids and timestamps, so an import
reproduces the history exactly rather than re-stamping it as "just imported".
Deployments whose alias has since left soroforge.yaml are still included —
the history outlives the config.

With --out the document is written to that file; otherwise it goes to stdout,
where ` + "`soroforge history export > history.json`" + ` does the rest.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			snap, err := store.BuildSnapshot(cmd.Context(), a.store)
			if err != nil {
				return err
			}
			data, err := snap.Marshal()
			if err != nil {
				return err
			}

			if outPath == "" {
				if _, err := out().Write(data); err != nil {
					return err
				}
				return nil
			}
			// 0644: the file is history data, not a secret — it holds only
			// public addresses and hashes by construction.
			if err := os.WriteFile(outPath, data, 0o644); err != nil {
				return fmt.Errorf("write history file: %w", err)
			}
			fmt.Fprintf(out(), "Exported %d contract(s) and %d deployment(s) to %s.\n",
				len(snap.Contracts), len(snap.Deployments), outPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&outPath, "out", "", "file to write (default: stdout)")
	return cmd
}

func newHistoryImportCmd() *cobra.Command {
	var merge bool

	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Load a history export into the database",
		Long: `Import a JSON document written by ` + "`history export`" + `.

Records keep their original ids and timestamps; the importer never re-stamps.
A database that already holds history is refused unless --merge is given,
which skips records identical to what is already stored and adds only what is
missing. Same-id-with-different-content is always an error: when the file and
the database disagree about an identity, guessing would corrupt one of them.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read history file: %w", err)
			}
			snap, err := store.ParseSnapshot(raw)
			if err != nil {
				return err
			}

			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			report, err := store.Import(cmd.Context(), a.store, snap, store.ImportOptions{Merge: merge})
			if err != nil {
				return err
			}
			fmt.Fprintf(out(),
				"Imported %d contract(s) and %d deployment(s) from %s (%d contract(s), %d deployment(s) already present, skipped).\n",
				report.ContractsImported, report.DeploymentsImported, args[0],
				report.ContractsSkipped, report.DeploymentsSkipped)
			return nil
		},
	}

	cmd.Flags().BoolVar(&merge, "merge", false,
		"import into a non-empty database, skipping records that exactly duplicate what is stored")
	return cmd
}
