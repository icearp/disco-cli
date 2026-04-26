package cmd

import (
	"context"
	"fmt"
	"io"
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
	// errCount > 0 means the service hit one or more errors (which were already
	// reported via OnError); annotate the line with "(with errors)" so the user
	// can scan output for trouble without grepping.
	var totalSeen, totalNew int64
	db.OnServiceComplete = func(service string, total, inserted, errCount int) {
		atomic.AddInt64(&totalSeen, int64(total))
		atomic.AddInt64(&totalNew, int64(inserted))
		if quiet {
			return
		}
		suffix := ""
		if errCount > 0 {
			suffix = "  (with errors)"
		}
		_, _ = fmt.Fprintf(progressW, "  [%s] %-*s  (%d total, %d new)%s\n",
			time.Since(start).Round(time.Second), nameWidth, service, total, inserted, suffix)
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

	// Collect errors in memory and render grouped at end. Errors do NOT abort
	// the scan: any provider/service/resolver failure is captured here and
	// surfaced exactly once after all in-flight work settles.
	var (
		scanErrors []store.ScanError
		errMu      sync.Mutex
	)
	db.OnError = func(e store.ScanError) {
		errMu.Lock()
		scanErrors = append(scanErrors, e)
		errMu.Unlock()
	}

	// Run all providers in parallel. A failure in one provider does not abort
	// its siblings — security users would rather have a partial inventory from
	// the providers that succeeded than nothing at all. Providers should never
	// return an error from Scan() (errors flow through OnError); if one does,
	// we still capture it as a ScanError and continue.
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, s := range scanners {
		wg.Go(func() {
			if err := s.Scan(ctx, db, scanID); err != nil {
				errMu.Lock()
				scanErrors = append(scanErrors, store.ScanError{
					Provider: s.Name(), Service: "scan", Scope: "", Message: err.Error(),
				})
				errMu.Unlock()
			}
		})
	}
	wg.Wait()

	// Render grouped warnings + errors blocks before the final summary line.
	renderWarnings(progressW, warnings, quiet)
	renderErrors(progressW, scanErrors, quiet)

	warnSuffix := ""
	if len(warnings) > 0 {
		warnSuffix = fmt.Sprintf(", %d warnings", len(warnings))
	}
	errSuffix := ""
	if len(scanErrors) > 0 {
		errSuffix = fmt.Sprintf(", %d errors", len(scanErrors))
	}

	count := int(totalSeen)

	// Any errors → partial scan; otherwise complete. We no longer distinguish
	// "all failed" from "some failed" because nothing aborts: even with errors,
	// resources from the surviving services are persisted and worth keeping.
	if len(scanErrors) > 0 {
		errMsgs := make([]string, len(scanErrors))
		for i, e := range scanErrors {
			errMsgs[i] = fmt.Sprintf("%s/%s: %s", e.Provider, e.Service, e.Message)
		}
		errMsg := strings.Join(errMsgs, "; ")
		if perr := db.PartialScan(scanID, count, errMsg); perr != nil {
			return fmt.Errorf("mark partial scan: %w", perr)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Scan partial: %d resources (%d new) in %s%s%s\n",
			count, int(totalNew), time.Since(start).Round(time.Second), warnSuffix, errSuffix)
		return nil
	}

	if err := db.CompleteScan(scanID, count); err != nil {
		return fmt.Errorf("complete scan: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Scan complete: %d resources (%d new) in %s%s\n",
		count, int(totalNew), time.Since(start).Round(time.Second), warnSuffix)
	return nil
}

// renderErrors prints a grouped, column-aligned block of scan errors at the
// end of the run so each failure is shown exactly once.
func renderErrors(w io.Writer, errs []store.ScanError, quiet bool) {
	rows := make([]messageRow, len(errs))
	for i, e := range errs {
		rows[i] = messageRow{e.Provider, e.Service, e.Scope, e.Message}
	}
	renderMessages(w, "Errors", rows, quiet)
}

// renderWarnings prints a grouped, column-aligned block of scan warnings.
func renderWarnings(w io.Writer, warnings []store.ScanWarning, quiet bool) {
	rows := make([]messageRow, len(warnings))
	for i, x := range warnings {
		rows[i] = messageRow{x.Provider, x.Service, x.Scope, x.Message}
	}
	renderMessages(w, "Warnings", rows, quiet)
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
