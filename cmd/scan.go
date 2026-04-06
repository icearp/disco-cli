package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

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
  disco scan                          # scans all configured providers
  disco scan --providers aws,gcp`,
	RunE: func(cmd *cobra.Command, args []string) error {
		names, _ := cmd.Flags().GetStringSlice("providers")
		if len(names) == 0 {
			return runScan(cmd, providers.All())
		}
		// Resolve the named providers, erroring on any unknown name.
		scanners := make([]providers.Scanner, 0, len(names))
		for _, name := range names {
			s, ok := providers.Get(name)
			if !ok {
				return fmt.Errorf("unknown provider %q (available: %s)", name, strings.Join(providers.Names(), ", "))
			}
			scanners = append(scanners, s)
		}
		return runScan(cmd, scanners)
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
	start := time.Now()
	fmt.Fprintf(cmd.OutOrStdout(), "Scan %s started: %v\n", scanID, start.Round(time.Second))

	// Print a status line each time a provider completes scanning one service.
	db.OnServiceComplete = func(service string) {
		fmt.Fprintf(cmd.OutOrStdout(), "  [%s] %s done\n",
			time.Since(start).Round(time.Second), service)
	}

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
	fmt.Fprintf(cmd.OutOrStdout(), "Scan complete: %d resources in %s\n",
		count, time.Since(start).Round(time.Second))
	return nil
}

func init() {
	scanCmd.Flags().StringSlice("providers", nil, "comma-separated provider(s) to scan (e.g. aws,gcp); omit to scan all")

	// Add one subcommand per registered provider so users can run e.g. "disco scan aws".
	// providers.All() is populated by init()s in cmd/providers.go's blank imports,
	// which are guaranteed to run before this init().
	for _, s := range providers.All() {
		s := s
		subcmd := &cobra.Command{
			Use:   s.Name(),
			Short: fmt.Sprintf("Scan %s resources", s.Name()),
		}
		subcmd.Flags().StringSlice("services", nil,
			fmt.Sprintf("comma-separated services to scan (e.g. %s:ec2,%s:iam); omit to scan all", s.Name(), s.Name()))
		subcmd.RunE = func(cmd *cobra.Command, _ []string) error {
			svcs, _ := cmd.Flags().GetStringSlice("services")
			if len(svcs) > 0 {
				// Apply filter if the provider supports it.
				if sf, ok := s.(providers.ServiceFilterer); ok {
					sf.SetServiceFilter(svcs)
				}
			}
			return runScan(cmd, []providers.Scanner{s})
		}
		scanCmd.AddCommand(subcmd)
	}
	rootCmd.AddCommand(scanCmd)
}
