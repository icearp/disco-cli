package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
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
		// Positional args are not supported; catch "disco scan aws,gcp" mistakes.
		if len(args) > 0 {
			return fmt.Errorf("unexpected argument %q — use 'disco scan <provider>' for a single provider or 'disco scan --providers %s' for a subset",
				args[0], strings.Join(args, ","))
		}
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
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No providers registered — nothing to scan.")
		return nil
	}

	db, err := store.Open(defaultDBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

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

	// Progress goes to stderr so stdout stays clean for piping (only the
	// final summary line and the scan ID land on stdout). --quiet silences
	// the per-service progress but keeps the summary.
	quiet, _ := cmd.Flags().GetBool("quiet")
	progressW := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(progressW, "Scan %s started: %v\n", scanID, start.Round(time.Second))

	// Compute the longest service name across all in-scope scanners for column alignment.
	nameWidth := 0
	for _, s := range scanners {
		if sn, ok := s.(providers.ServiceNamer); ok {
			for _, name := range sn.ServiceNames() {
				if len(name) > nameWidth {
					nameWidth = len(name)
				}
			}
		}
	}

	// Print a status line each time a provider completes scanning one service.
	// Accumulate totals here — these are the source of truth for the summary counts.
	var totalSeen, totalNew int64
	db.OnServiceComplete = func(service string, total, inserted int) {
		atomic.AddInt64(&totalSeen, int64(total))
		atomic.AddInt64(&totalNew, int64(inserted))
		if quiet {
			return
		}
		_, _ = fmt.Fprintf(progressW, "  [%s] %-*s  (%d total, %d new)\n",
			time.Since(start).Round(time.Second), nameWidth, service, total, inserted)
	}
	// Print a message when the resolver phase starts and a summary when it finishes.
	db.OnResolveStart = func(provider string) {
		if quiet {
			return
		}
		_, _ = fmt.Fprintf(progressW, "  [%s] %s: resolving relationships...\n",
			time.Since(start).Round(time.Second), provider)
	}
	db.OnResolveComplete = func(provider string, edges int) {
		if quiet {
			return
		}
		_, _ = fmt.Fprintf(progressW, "  [%s] %s: relationships resolved (%d edges)\n",
			time.Since(start).Round(time.Second), provider, edges)
	}

	// Collect non-fatal skip warnings in memory and render grouped at end.
	var (
		warnings []store.ScanWarning
		warnMu   sync.Mutex
	)
	db.OnWarn = func(w store.ScanWarning) {
		warnMu.Lock()
		warnings = append(warnings, w)
		warnMu.Unlock()
	}

	// Run all providers in parallel. A failure in one provider no longer
	// cancels its siblings — security users would rather have a partial
	// inventory from the providers that succeeded than nothing at all.
	ctx := context.Background()
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		failed  []string // formatted "<provider>: <error>"
		succeed int
	)
	for _, s := range scanners {
		wg.Go(func() {
			if err := s.Scan(ctx, db, scanID); err != nil {
				mu.Lock()
				failed = append(failed, fmt.Sprintf("%s: %v", s.Name(), err))
				mu.Unlock()
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  [%s] %s: FAILED: %v\n",
					time.Since(start).Round(time.Second), s.Name(), err)
				return
			}
			mu.Lock()
			succeed++
			mu.Unlock()
		})
	}
	wg.Wait()

	// Render grouped warnings block before the final summary line.
	renderWarnings(progressW, warnings, quiet)
	warnSuffix := ""
	if len(warnings) > 0 {
		warnSuffix = fmt.Sprintf(", %d warnings", len(warnings))
	}

	count := int(totalSeen)
	errMsg := strings.Join(failed, "; ")

	// All providers failed → mark failed and surface combined error to the shell.
	if succeed == 0 && len(failed) > 0 {
		if ferr := db.FailScan(scanID, errMsg); ferr != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not record scan failure: %v\n", ferr)
		}
		return fmt.Errorf("scan failed: %s", errMsg)
	}

	// Some providers failed but others succeeded → partial scan.
	if len(failed) > 0 {
		if perr := db.PartialScan(scanID, count, errMsg); perr != nil {
			return fmt.Errorf("mark partial scan: %w", perr)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Scan partial: %d resources (%d new) in %s%s; failed providers: %s\n",
			count, int(totalNew), time.Since(start).Round(time.Second), warnSuffix, errMsg)
		return nil
	}

	if err := db.CompleteScan(scanID, count); err != nil {
		return fmt.Errorf("complete scan: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Scan complete: %d resources (%d new) in %s%s\n",
		count, int(totalNew), time.Since(start).Round(time.Second), warnSuffix)
	return nil
}

// renderWarnings prints a grouped, column-aligned block of scan warnings.
// Sort order: provider, scope, service — deterministic across runs.
func renderWarnings(w io.Writer, warnings []store.ScanWarning, quiet bool) {
	if quiet || len(warnings) == 0 {
		return
	}
	sorted := make([]store.ScanWarning, len(warnings))
	copy(sorted, warnings)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Provider != sorted[j].Provider {
			return sorted[i].Provider < sorted[j].Provider
		}
		if sorted[i].Scope != sorted[j].Scope {
			return sorted[i].Scope < sorted[j].Scope
		}
		return sorted[i].Service < sorted[j].Service
	})
	// Compute widths for column alignment.
	provW, svcW, scopeW := 0, 0, 0
	for _, x := range sorted {
		if len(x.Provider) > provW {
			provW = len(x.Provider)
		}
		if len(x.Service) > svcW {
			svcW = len(x.Service)
		}
		if len(x.Scope) > scopeW {
			scopeW = len(x.Scope)
		}
	}
	_, _ = fmt.Fprintf(w, "\nWarnings (%d):\n", len(sorted))
	for _, x := range sorted {
		_, _ = fmt.Fprintf(w, "  %-*s  %-*s  %-*s  %s\n",
			provW, x.Provider, svcW, x.Service, scopeW, x.Scope, x.Message)
	}
}

func init() {
	scanCmd.Flags().StringSlice("providers", nil, "comma-separated provider(s) to scan (e.g. aws,gcp); omit to scan all")
	// Persistent so subcommands (disco scan aws, etc.) inherit the flag.
	scanCmd.PersistentFlags().Bool("quiet", false, "suppress per-service progress output; only print the final summary")

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
		subcmd.Flags().StringSlice("region", nil,
			"regions to scan, comma-separated (overrides config; e.g. us-west-2,eu-west-1)")
		subcmd.Flags().String("profile", "",
			"named credential profile (e.g. a profile defined in ~/.aws/config)")
		subcmd.RunE = func(cmd *cobra.Command, _ []string) error {
			svcs, _ := cmd.Flags().GetStringSlice("services")
			if len(svcs) > 0 {
				// Apply filter if the provider supports it.
				if sf, ok := s.(providers.ServiceFilterer); ok {
					sf.SetServiceFilter(svcs)
				}
			}
			regions, _ := cmd.Flags().GetStringSlice("region")
			if len(regions) > 0 {
				if ro, ok := s.(providers.RegionOverrider); ok {
					ro.SetRegionOverride(regions)
				}
			}
			profile, _ := cmd.Flags().GetString("profile")
			if profile != "" {
				if po, ok := s.(providers.ProfileOverrider); ok {
					po.SetProfile(profile)
				}
			}
			return runScan(cmd, []providers.Scanner{s})
		}
		scanCmd.AddCommand(subcmd)
	}
	rootCmd.AddCommand(scanCmd)
}
