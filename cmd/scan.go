package cmd

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan cloud accounts and discover resources",
	Long: `Scan one or more cloud providers and store discovered resources in the local database.

Examples:
  disco scan aws
  disco scan gcp
  disco scan       # scans all configured providers`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScan(cmd, providers.All())
	},
}

// runScan executes a scan for the given set of scanners: opens the database,
// records a scan row, runs all scanners in parallel, then marks the scan
// complete or failed.
func runScan(cmd *cobra.Command, scanners []providers.Scanner) error {
	if len(scanners) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No providers registered — nothing to scan.")
		return nil
	}

	db, err := store.Open(defaultDBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Collect names for the scan record.
	names := make([]string, len(scanners))
	for i, s := range scanners {
		names[i] = s.Name()
	}

	scanID, err := db.CreateScan(names, map[string]any{"providers": names})
	if err != nil {
		return fmt.Errorf("create scan record: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Scan %s started: %v\n", scanID, names)

	// Run all providers in parallel; cancel siblings on the first error.
	ctx := context.Background()
	g, ctx := errgroup.WithContext(ctx)
	for _, s := range scanners {
		g.Go(func() error { return s.Scan(ctx, db, scanID) })
	}

	if err := g.Wait(); err != nil {
		// Best-effort: mark the scan as failed before returning the error.
		if ferr := db.FailScan(scanID, err.Error()); ferr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not record scan failure: %v\n", ferr)
		}
		return err
	}

	count, err := db.CountResourcesByScan(scanID)
	if err != nil {
		return fmt.Errorf("count resources: %w", err)
	}
	if err := db.CompleteScan(scanID, count); err != nil {
		return fmt.Errorf("complete scan: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Scan %s complete.\n", scanID)
	return nil
}

func init() {
	// Add one subcommand per registered provider so users can run e.g. "disco scan aws".
	// providers.All() is populated by init()s in cmd/providers.go's blank imports,
	// which are guaranteed to run before this init().
	for _, s := range providers.All() {
		scanCmd.AddCommand(&cobra.Command{
			Use:   s.Name(),
			Short: fmt.Sprintf("Scan %s resources", s.Name()),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runScan(cmd, []providers.Scanner{s})
			},
		})
	}
	rootCmd.AddCommand(scanCmd)
}
