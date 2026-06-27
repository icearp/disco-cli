package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/scanrun"
	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Terminal-state exit-code sentinels. The summary line is already written to
// stdout; these only carry the exit-code gate, so cmd/root.go::Execute maps
// them without re-printing (mirrors errFindingsReported). errScanInterrupted →
// 130 (conventional SIGINT code); errScanPartial → 1, emitted only under
// --fail-on-error so the default partial-run behaviour stays exit 0.
var (
	errScanInterrupted = errors.New("scan interrupted")
	errScanPartial     = errors.New("scan completed with errors")
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan cloud accounts and discover resources",
	Long: `Scan one or more cloud providers and store discovered resources in the local database.

Three invocation forms — pick by what you need to tune:

  disco scan <provider>         scope to one provider with that provider's
                                full flag surface (--regions, --profile,
                                --services, --skip-globals where supported).
                                Use this for AWS region selection, AWS
                                profile switching, or any single-provider
                                tuned scan.

  disco scan --providers a,b    scope to a subset of providers without
                                provider-specific flags. Useful in CI or
                                when config.yaml carries the per-provider
                                tuning and the runner only needs to pick
                                which providers to fan out across.

  disco scan                    fan out to every provider configured (or
                                auto-detected via ambient creds) — the
                                "scan everything I can reach" form.

--if-older-than gates any of the above on recency: skip (exit 0) when
the latest complete scan for every targeted provider is younger than
the supplied duration. Suitable for cron drivers that should run hourly
but only re-scan if 6 h have elapsed.`,
	Example: `  disco scan aws
  disco scan gcp
  disco scan                          # scans all configured providers
  disco scan --providers aws,gcp
  disco scan aws --if-older-than 6h   # skip if last aws scan finished < 6h ago`,
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
				return fmt.Errorf("unknown provider %q (available: %s)", name, providerListHint())
			}
			scanners = append(scanners, s)
		}
		return runScan(cmd, scanners)
	},
}

// startOrResumeScan picks the scan_id for this run. resumeFlag values:
//   - "" (default) — fresh scan via CreateScan.
//   - "latest" — pick up the most-recent scan whose status is running/partial.
//   - any other value — treated as an explicit scan_id to reuse.
//
// Returns (scanID, resuming, err). When resuming, callers should expect the
// checkpoint table to carry per-service watermarks a future incremental
// scanner can consume; today the scan persists fresh checkpoints from this
// scan_id without consuming them.
func startOrResumeScan(db *store.Store, resumeFlag string, providers []string, scope map[string]any) (string, bool, error) {
	if resumeFlag == "" {
		if scope == nil {
			scope = map[string]any{"providers": providers}
		}
		// An external orchestrator can set DISCO_SCAN_ID so its audit trail,
		// scans and resources share one identifier (chain-of-custody). Empty
		// falls back to a freshly-minted 32-hex id.
		idHint := strings.TrimSpace(os.Getenv("DISCO_SCAN_ID"))
		id, err := db.CreateScanWithID(idHint, providers, scope)
		if err != nil {
			return "", false, fmt.Errorf("create scan record: %w", err)
		}
		return id, false, nil
	}
	if resumeFlag == "latest" {
		sc, err := db.LatestIncompleteScan()
		if err != nil {
			return "", false, fmt.Errorf("--resume latest: no incomplete scan found: %w", err)
		}
		return sc.ID, true, nil
	}
	if _, err := db.GetScan(resumeFlag); err != nil {
		return "", false, fmt.Errorf("--resume %s: scan not found: %w", resumeFlag, err)
	}
	return resumeFlag, true, nil
}

// runScan executes a scan for the given set of scanners: opens the database,
// records a scan row, runs all scanners in parallel, then marks the scan
// complete or failed.
func runScan(cmd *cobra.Command, scanners []providers.Scanner) error {
	if dbReadOnly {
		return fmt.Errorf("--db-readonly: scan cannot run in read-only mode")
	}
	if len(scanners) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No providers registered — nothing to scan.")
		return nil
	}

	// Collect names for the scan record / dry-run report.
	names := make([]string, len(scanners))
	for i, s := range scanners {
		names[i] = s.Name()
	}

	// --dry-run resolves --if-older-than + provider selection without opening
	// SDK clients or touching cloud APIs. Cron authors validate scheduling
	// logic in CI without burning live API quota.
	if handled, err := handleScanDryRun(cmd, names); handled {
		return err
	}

	// openWriteDB opens Postgres when DISCO_PG_DSN is set (the scan-worker
	// deployment); otherwise it falls back to SQLite at defaultDBPath() for
	// normal CLI / local-dev use.
	db, err := openWriteDB()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// --if-older-than gates the scan on recency: if the latest complete
	// (or partial) scan covering each requested provider is younger than the
	// threshold, skip with exit 0 + a stderr note. Cron-friendly idempotency
	// — drop a `disco scan aws --if-older-than 1h` into a 5-min cron and
	// only do real work when the cached state is stale.
	if d, _ := cmd.Flags().GetDuration("if-older-than"); d > 0 {
		skip, msg, err := evaluateIfOlderThan(db, names, d)
		if err != nil {
			return err
		}
		if skip {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), msg)
			return nil
		}
	}

	// --resume reuses a previously-started scan_id and its checkpoint set.
	// Without it (default), a fresh scan_id is generated. Today the scan
	// persists checkpoints and exposes the lookup; a future incremental
	// scanner can consume the per-page watermarks to skip already-listed
	// pages without re-scanning.
	resumeFlag, _ := cmd.Flags().GetString("resume")
	scope := buildScanScope(cmd, names, scanners)
	scanID, resuming, err := startOrResumeScan(db, resumeFlag, names, scope)
	if err != nil {
		return err
	}
	start := time.Now()
	// Progress goes to stderr so stdout stays clean for piping (only the final
	// summary line lands on stdout). --quiet silences the start/resume banners
	// and per-service progress but keeps the final summary.
	quiet, _ := cmd.Flags().GetBool("quiet")
	noProgress, _ := cmd.Flags().GetBool("no-progress")
	progressW := cmd.ErrOrStderr()
	if resuming && !quiet {
		cps, lerr := db.ListCheckpoints(scanID)
		if lerr == nil {
			_, _ = fmt.Fprintf(progressW,
				"Resuming scan %s with %d checkpoint(s)\n", scanID, len(cps))
		}
	}
	if !quiet {
		_, _ = fmt.Fprintf(progressW, "Scan %s started: %v\n", scanID, start.Round(time.Second))
	}

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

	scopeWidth := scopeColumnWidth(scanners)

	// Print a status line each time a provider completes scanning one service.
	// scope = the per-call dimension that would otherwise produce duplicate
	// lines in multi-region / multi-account scans (AWS region or "global",
	// Azure subscription ID, GCP project ID). Without it, --regions us-east-1,
	// eu-west-1 prints aws:ec2 twice with no way to tell them apart.
	// errCount > 0 means the service hit one or more errors (which were already
	// reported via OnError); annotate the line with "(with errors)" so the user
	// can scan output for trouble without grepping. Totals are accumulated by
	// scanrun.RunScanners (single source of truth shared with the API driver).
	// p streams the permanent per-service / resolve lines and, on an interactive
	// terminal, animates a spinner so a slow service or the resolve phase still
	// shows liveness. No done/total fraction: the denominator (services × scopes)
	// isn't knowable up front (Azure subscriptions and GCP projects are
	// discovered at scan time), so the spinner reports elapsed + completed-unit
	// count only — honest, not a fake %. The spinner is suppressed off-TTY (CI),
	// under --no-progress, and under --quiet. incDone is atomic because
	// RunScanners fans scanners out concurrently, so these closures run from
	// multiple goroutines.
	spinnerOn := !quiet && !noProgress && isTerminal(progressW)
	p := newProgress(progressW, start, spinnerOn)
	defer p.stop()
	db.OnServiceComplete = func(service, scope string, total, newCount, changed, errCount int, status store.ServiceStatus) {
		p.incDone()
		if quiet {
			return
		}
		suffix := serviceStatusSuffix(status, errCount)
		p.line(fmt.Sprintf("  %s %-*s  %-*s  (%d total, %d new, %d changed)%s",
			elapsedField(time.Since(start)), nameWidth, service, scopeWidth, scope, total, newCount, changed, suffix))
	}
	// Print a message when the resolver phase starts and a summary when it finishes.
	db.OnResolveStart = func(provider string) {
		if quiet {
			return
		}
		p.line(fmt.Sprintf("  %s %s: resolving relationships...",
			elapsedField(time.Since(start)), provider))
	}
	db.OnResolveComplete = func(provider string, edges int) {
		if quiet {
			return
		}
		p.line(fmt.Sprintf("  %s %s: relationships resolved (%d edges)",
			elapsedField(time.Since(start)), provider, edges))
	}

	// Fan-out + warning/error capture + total accumulation live in scanrun so
	// the engine can be reused by other drivers. RunScanners chains its
	// callbacks on top of the OnServiceComplete progress callback installed
	// above and returns the canonical totals.
	// cmd.Context() carries main's SIGINT/SIGTERM cancellation, so an
	// interrupted scan unwinds (scanners honor ctx on their SDK calls) and the
	// deferred db.Close() still runs the WAL checkpoint+cleanup.
	ctx := cmd.Context()
	warnings, scanErrors, totalSeen, totalNew, totalChanged := scanrun.RunScanners(ctx, db, scanID, scanners)

	// Stop the spinner and clear its transient line before any other writer
	// touches stderr (warnings/errors below, summary on stdout).
	p.stop()

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

	// Finalize owns the Complete/Partial dispatch + structured-error
	// persistence, shared with the scanrun.Execute API driver. A cancelled ctx
	// (SIGINT/SIGTERM) forces partial even if no per-service error was reported.
	res, ferr := scanrun.Finalize(db, scanID, totalSeen, scanErrors, ctx.Err() != nil)
	if ferr != nil {
		return ferr
	}
	for _, aerr := range res.AppendErrors {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "append scan error: %v\n", aerr)
	}

	if res.Interrupted {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Scan interrupted: %d resources (%d new, %d changed) in %s%s%s\n",
			totalSeen, totalNew, totalChanged, time.Since(start).Round(time.Second), warnSuffix, errSuffix)
		return errScanInterrupted
	}
	if res.Partial {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Scan partial: %d resources (%d new, %d changed) in %s%s%s\n",
			totalSeen, totalNew, totalChanged, time.Since(start).Round(time.Second), warnSuffix, errSuffix)
		if failOnError, _ := cmd.Flags().GetBool("fail-on-error"); failOnError {
			return errScanPartial
		}
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Scan complete: %d resources (%d new, %d changed) in %s%s\n",
		totalSeen, totalNew, totalChanged, time.Since(start).Round(time.Second), warnSuffix)
	return nil
}

// elapsedField renders the scan-elapsed time as an 8-char-wide bracketed column
// ("[45s]   ") so the #N counter and columns after it don't shift as the scan
// runs. The brackets hug the natural Duration string; padding sits to the RIGHT
// of "]". Values up to ~1h fit in 8 ("[10m23s]"); a scan past ~1h (e.g.
// "[1h2m3s]" = 8, "[1h40m0s]" = 9) overflows by a char — rare, scans are minutes.
func elapsedField(d time.Duration) string {
	return fmt.Sprintf("%-8s", "["+d.Round(time.Second).String()+"]")
}

// serviceStatusSuffix renders the trailing annotation on a per-service scan
// progress line. ServiceUnavailable (not deployed in this AWS region) and
// ServiceDisabled (account hasn't enabled it) are mutually exclusive with the
// error suffix, since a skipped service produces no errors.
func serviceStatusSuffix(status store.ServiceStatus, errCount int) string {
	switch {
	case status == store.ServiceUnavailable:
		return "  (service unavailable)"
	case status == store.ServiceDisabled:
		return "  (service disabled)"
	case errCount > 0:
		return "  (with errors)"
	}
	return ""
}

// scopeColumnWidth returns the padding width for the per-line scope column
// in scan progress output, sized to the worst-case scope shape across the
// enabled providers. Region scopes come from each provider's static
// RegionNamer list; non-region scope shapes (Azure subscription UUID, GCP
// project ID) carry per-provider baselines because they aren't in the
// region list. "global" / "tenant" / "org" rows slot into the same column
// with trailing padding, keeping the (N total, N new) counts at a fixed
// column.
func scopeColumnWidth(scanners []providers.Scanner) int {
	width := len("global")
	for _, s := range scanners {
		if rn, ok := s.(providers.RegionNamer); ok {
			for _, r := range rn.RegionNames() {
				if len(r) > width {
					width = len(r)
				}
			}
		}
		if sw, ok := s.(providers.ScopeColumnWidther); ok {
			if w := sw.ScopeColumnWidth(); w > width {
				width = w
			}
		}
	}
	return width
}

// buildScanScope captures the resolved per-provider scope (regions, profile,
// services, skip_globals) into a map suitable for the scans.scope JSON
// column. Always emits positive defaults (`regions:"all"`, `profile:"default"`)
// rather than omitting unset flags, so an audit trail can answer "what did
// the operator actually scan?" without "absence-of-flag-implies-default"
// guesswork. Only the per-provider subcommand's cmd carries the scoping
// flags; the multi-provider parent path receives a baseline scope keyed by
// provider names.
func buildScanScope(cmd *cobra.Command, names []string, scanners []providers.Scanner) map[string]any {
	scope := map[string]any{"providers": names}
	if len(scanners) != 1 {
		return scope
	}
	s := scanners[0]
	provScope := map[string]any{}
	if _, ok := s.(providers.RegionOverrider); ok {
		regions, _ := cmd.Flags().GetStringSlice("regions")
		if len(regions) > 0 {
			provScope["regions"] = regions
		} else {
			provScope["regions"] = "all"
		}
	}
	if _, ok := s.(providers.ProfileOverrider); ok {
		profile, _ := cmd.Flags().GetString("profile")
		if profile == "" {
			profile = "default"
		}
		provScope["profile"] = profile
	}
	if _, ok := s.(providers.RoleOverrider); ok {
		// Record only the ARN — external_id is treated like a credential
		// and never lands in the audit-trail JSON.
		if roleARN, _ := cmd.Flags().GetString("role-arn"); roleARN != "" {
			provScope["role_arn"] = roleARN
		}
	}
	if _, ok := s.(providers.SourceIdentityOverrider); ok {
		// SourceIdentity is an audit value (not a credential), so it belongs in
		// the scan scope. Record the configured token ("auto" or a literal); the
		// resolved scan-ID form is the scan record's own ID.
		sourceIdentity, _ := cmd.Flags().GetString("source-identity")
		if sourceIdentity == "" {
			sourceIdentity = viper.GetString(s.Name() + ".source_identity")
		}
		if sourceIdentity != "" {
			provScope["source_identity"] = sourceIdentity
		}
	}
	if _, ok := s.(providers.RegionScopeToggler); ok {
		provScope["scope_to_available_regions"] = scopeRegionsEnabled(cmd, s.Name())
	}
	if _, ok := s.(providers.ServiceQuotasIncluder); ok {
		include := false
		if cmd.Flags().Changed("include-service-quotas") {
			include, _ = cmd.Flags().GetBool("include-service-quotas")
		} else if viper.IsSet(s.Name() + ".include_service_quotas") {
			include = viper.GetBool(s.Name() + ".include_service_quotas")
		}
		provScope["include_service_quotas"] = include
	}
	if _, ok := s.(providers.SubscriptionOverrider); ok {
		if subs, _ := cmd.Flags().GetStringSlice("subscriptions"); len(subs) > 0 {
			provScope["subscriptions"] = subs
		}
	}
	if _, ok := s.(providers.ServiceFilterer); ok {
		svcs, _ := cmd.Flags().GetStringSlice("services")
		if len(svcs) > 0 {
			provScope["services"] = svcs
		} else {
			provScope["services"] = "all"
		}
	}
	if _, ok := s.(providers.GlobalsSkipper); ok {
		skip, _ := cmd.Flags().GetBool("skip-globals")
		provScope["skip_globals"] = skip
	}
	if len(provScope) > 0 {
		scope[s.Name()] = provScope
	}
	return scope
}

// dryRunDecision is one provider's would-scan/would-skip verdict. The ephemeral
// scan plan a --dry-run computes is never persisted, so -o json is the only
// machine-readable path to it (a live scan re-queries via 'disco scans').
type dryRunDecision struct {
	Provider  string `json:"provider"`
	WouldScan bool   `json:"would_scan"`
	Detail    string `json:"detail"`
}

// handleScanDryRun reads --dry-run / --output and validates their interaction.
// handled=true means runScan should return err immediately: either the dry-run
// plan ran, or --output was misused on a live scan. --output is scoped to
// --dry-run (a live scan reports via 'disco scans'), so a non-default value
// without --dry-run is rejected rather than silently ignored.
func handleScanDryRun(cmd *cobra.Command, names []string) (handled bool, err error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	output, _ := cmd.Flags().GetString("output")
	if !dryRun {
		if output != "" && output != "table" {
			return true, fmt.Errorf("--output %s applies only with --dry-run; query a live scan's result via 'disco scans -o %s'", output, output)
		}
		return false, nil
	}
	return true, runScanDryRun(cmd, names, output)
}

// runScanDryRun reports "would scan" / "would skip" decisions per provider
// without opening SDK clients or hitting cloud APIs. Reads the local DB
// read-only to evaluate --if-older-than against latest complete scans.
// Always exits 0 — pipeline operators read the decisions, not the exit code.
func runScanDryRun(cmd *cobra.Command, names []string, output string) error {
	if output != "" && output != "table" && output != "json" {
		return fmt.Errorf("--output %q not supported for --dry-run (use table or json)", output)
	}
	d, _ := cmd.Flags().GetDuration("if-older-than")
	decisions := make([]dryRunDecision, 0, len(names))

	db, err := store.OpenReadOnly(defaultDBPath())
	if err != nil {
		// First-run UX: report decisions even without a DB. Recency gate
		// trivially passes (no prior scan ⇒ scan must run).
		for _, name := range names {
			detail := "no recency gate"
			if d > 0 {
				detail = fmt.Sprintf("threshold %s, no DB on disk yet", d)
			}
			decisions = append(decisions, dryRunDecision{Provider: name, WouldScan: true, Detail: detail})
		}
		return renderDryRun(cmd, decisions, output)
	}
	defer func() { _ = db.Close() }()
	for _, name := range names {
		wouldScan := true
		detail := "no recency gate"
		if d > 0 {
			detail = fmt.Sprintf("threshold %s", d)
			sc, sErr := db.LatestCompleteScan(name)
			switch {
			case sErr != nil:
				detail = fmt.Sprintf("threshold %s, no prior complete scan", d)
			default:
				if t, perr := time.Parse("2006-01-02 15:04:05", sc.StartedAt); perr == nil {
					age := time.Since(t).Round(time.Second)
					if t.After(time.Now().UTC().Add(-d)) {
						wouldScan = false
						detail = fmt.Sprintf("latest=%s ago < %s", age, d)
					} else {
						detail = fmt.Sprintf("latest=%s ago >= %s", age, d)
					}
				}
			}
		}
		decisions = append(decisions, dryRunDecision{Provider: name, WouldScan: wouldScan, Detail: detail})
	}
	return renderDryRun(cmd, decisions, output)
}

// renderDryRun emits the dry-run plan as prose (default) or a JSON array.
func renderDryRun(cmd *cobra.Command, decisions []dryRunDecision, output string) error {
	if output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(decisions)
	}
	for _, dec := range decisions {
		verb := "would scan"
		if !dec.WouldScan {
			verb = "would skip"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", verb, dec.Provider, dec.Detail)
	}
	return nil
}

// evaluateIfOlderThan returns (skip, message, error). skip=true means the
// freshest qualifying scan for every requested provider is younger than d
// — so the caller should print message and exit 0. Any provider missing a
// recent scan keeps skip=false (the scan must run for that provider).
func evaluateIfOlderThan(db *store.Store, names []string, d time.Duration) (bool, string, error) {
	threshold := time.Now().UTC().Add(-d)
	youngest := time.Time{}
	for _, name := range names {
		sc, err := db.LatestCompleteScan(name)
		if err != nil {
			// No prior scan for this provider — must run.
			return false, "", nil
		}
		t, perr := time.Parse("2006-01-02 15:04:05", sc.StartedAt)
		if perr != nil {
			// Unparseable timestamp — be safe and run rather than skip.
			_, _ = fmt.Fprintf(os.Stderr, "--if-older-than: cannot parse scan %s started_at %q: %v (running scan)\n", sc.ID, sc.StartedAt, perr)
			return false, "", nil
		}
		if t.Before(threshold) {
			return false, "", nil
		}
		if t.After(youngest) {
			youngest = t
		}
	}
	age := time.Since(youngest).Round(time.Second)
	return true, fmt.Sprintf("scan skipped: latest scan %s ago (threshold %s)", age, d), nil
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

// scopeRegionsEnabled resolves the effective region-scoping setting for a
// provider: default on, overridden by the explicit --scope-regions flag, then
// the per-provider config key, and finally the negative --no-scope-regions
// alias (mirrors --no-progress) which forces it off when set. Shared by the
// scan-record scope builder and the SetRegionScope call so both stay in
// lockstep.
func scopeRegionsEnabled(cmd *cobra.Command, provider string) bool {
	enabled := true
	if cmd.Flags().Changed("scope-regions") {
		enabled, _ = cmd.Flags().GetBool("scope-regions")
	} else if viper.IsSet(provider + ".scope_to_available_regions") {
		enabled = viper.GetBool(provider + ".scope_to_available_regions")
	}
	if noScope, _ := cmd.Flags().GetBool("no-scope-regions"); noScope {
		enabled = false
	}
	return enabled
}

// registerScannerFlags adds the optional flags a provider's subcommand supports,
// gated on the matching capability interface — keeps --help honest (no flags
// listed that would be silently ignored). Extracted from init() to keep that
// function under the cyclomatic-complexity bar.
func registerScannerFlags(subcmd *cobra.Command, s providers.Scanner) {
	if _, ok := s.(providers.ServiceFilterer); ok {
		example := serviceFilterExample(s)
		subcmd.Flags().StringSlice("services", nil,
			fmt.Sprintf("Comma-separated %s services to scan (e.g. %s); omit to scan all", s.Name(), example))
	}
	if _, ok := s.(providers.RegionOverrider); ok {
		subcmd.Flags().StringSlice("regions", nil,
			"Regions to scan, comma-separated (overrides config; e.g. us-west-2,eu-west-1)")
	}
	if _, ok := s.(providers.ProfileOverrider); ok {
		subcmd.Flags().String("profile", "",
			"Named credential profile (e.g. a profile defined in ~/.aws/config)")
	}
	if _, ok := s.(providers.RoleOverrider); ok {
		subcmd.Flags().String("role-arn", "",
			"AssumeRole target — pins the scan to a single account reached by assuming this role; bypasses the config file's accounts: section")
		subcmd.Flags().String("external-id", "",
			"STS ExternalId for --role-arn (only honoured when --role-arn is also set)")
	}
	if _, ok := s.(providers.SourceIdentityOverrider); ok {
		subcmd.Flags().String("source-identity", "",
			"STS SourceIdentity stamped on assumed-role sessions for CloudTrail attribution; \"auto\" uses the scan ID. Requires the target role's trust policy to allow sts:SetSourceIdentity")
	}
	if _, ok := s.(providers.RegionScopeToggler); ok {
		subcmd.Flags().Bool("scope-regions", true,
			"Skip services in regions where the cloud doesn't offer them (via the SSM global-infrastructure catalog); fail-open. Disable with --no-scope-regions")
		subcmd.Flags().Bool("no-scope-regions", false,
			"Disable region scoping (negative alias for --scope-regions=false; mirrors --no-progress)")
	}
	if _, ok := s.(providers.ServiceQuotasIncluder); ok {
		subcmd.Flags().Bool("include-service-quotas", false,
			"Also scan aws:servicequotas (account quota limits); skipped by default — slow and separate from resource discovery. Or select it explicitly with --services aws:servicequotas")
	}
	if _, ok := s.(providers.SubscriptionOverrider); ok {
		subcmd.Flags().StringSlice("subscriptions", nil,
			"Subscription IDs to scan, comma-separated — pins the scan to exactly these and disables auto-enumeration; bypasses the config file's subscriptions: section")
	}
	if _, ok := s.(providers.GlobalsSkipper); ok {
		subcmd.Flags().Bool("skip-globals", false,
			"Skip services whose scope is account-wide (e.g. AWS IAM, Route53, CloudFront); regional services unaffected")
	}
	if _, ok := s.(providers.CredentialConfigOverrider); ok {
		subcmd.Flags().String("credential-config", "",
			"Path to a credential-configuration file: a keyless GCP Workload Identity Federation cred-config (from 'gcloud iam workload-identity-pools create-cred-config') or a service-account key; overrides the config file")
	}
}

func init() {
	scanCmd.Flags().StringSlice("providers", nil, "Comma-separated provider(s) to scan (e.g. aws,gcp); omit to scan all")
	_ = scanCmd.RegisterFlagCompletionFunc("providers", completeProviderNames)
	// Persistent so subcommands (disco scan aws, etc.) inherit the flag.
	scanCmd.PersistentFlags().Bool("quiet", false, "Suppress per-service progress output; only print the final summary")
	scanCmd.PersistentFlags().Bool("no-progress", false, "Disable the animated progress spinner; per-service lines still print")
	scanCmd.PersistentFlags().String("resume", "", "Resume a previous scan: pass a scan ID, or 'latest' to pick the most recent incomplete scan")
	scanCmd.PersistentFlags().Duration("if-older-than", 0,
		"Skip the scan (exit 0) when the latest complete scan for every targeted provider is younger than this duration (e.g. 1h, 24h)")
	scanCmd.PersistentFlags().Bool("dry-run", false,
		"Resolve provider selection + --if-older-than and print 'would scan / would skip' decisions; no SDK clients constructed, no cloud APIs called")
	scanCmd.PersistentFlags().StringP("output", "o", "table",
		"Output format for --dry-run decisions: table or json (a live scan reports via 'disco scans', not inline)")
	_ = scanCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "json"))
	scanCmd.PersistentFlags().Bool("fail-on-error", false,
		"Exit non-zero when a scan finishes partial (one or more services errored); default exit 0 on partial. SIGINT always exits 130")

	// Add one subcommand per registered provider so users can run e.g. "disco scan aws".
	// providers.All() is populated by init()s in cmd/providers.go's blank imports,
	// which are guaranteed to run before this init().
	for _, s := range providers.All() {
		subcmd := &cobra.Command{
			Use:   s.Name(),
			Short: fmt.Sprintf("Scan %s resources", s.Name()),
			Long:  scanProviderLong(s),
		}
		registerScannerFlags(subcmd, s)
		subcmd.RunE = func(cmd *cobra.Command, _ []string) error {
			if sf, ok := s.(providers.ServiceFilterer); ok {
				if svcs, _ := cmd.Flags().GetStringSlice("services"); len(svcs) > 0 {
					sf.SetServiceFilter(svcs)
				}
			}
			if ro, ok := s.(providers.RegionOverrider); ok {
				regions, _ := cmd.Flags().GetStringSlice("regions")
				if len(regions) > 0 {
					ro.SetRegionOverride(regions)
				}
			}
			if po, ok := s.(providers.ProfileOverrider); ok {
				if profile, _ := cmd.Flags().GetString("profile"); profile != "" {
					po.SetProfile(profile)
				}
			}
			if ro, ok := s.(providers.RoleOverrider); ok {
				roleARN, _ := cmd.Flags().GetString("role-arn")
				externalID, _ := cmd.Flags().GetString("external-id")
				if roleARN != "" {
					ro.SetRoleOverride(roleARN, externalID)
				}
			}
			if si, ok := s.(providers.SourceIdentityOverrider); ok {
				sourceIdentity, _ := cmd.Flags().GetString("source-identity")
				if sourceIdentity == "" {
					sourceIdentity = viper.GetString(s.Name() + ".source_identity")
				}
				if sourceIdentity != "" {
					si.SetSourceIdentity(sourceIdentity)
				}
			}
			if rst, ok := s.(providers.RegionScopeToggler); ok {
				rst.SetRegionScope(scopeRegionsEnabled(cmd, s.Name()))
			}
			if sqi, ok := s.(providers.ServiceQuotasIncluder); ok {
				// Default off; an explicit flag wins over the config key.
				include := false
				if cmd.Flags().Changed("include-service-quotas") {
					include, _ = cmd.Flags().GetBool("include-service-quotas")
				} else if viper.IsSet(s.Name() + ".include_service_quotas") {
					include = viper.GetBool(s.Name() + ".include_service_quotas")
				}
				sqi.SetIncludeServiceQuotas(include)
			}
			if so, ok := s.(providers.SubscriptionOverrider); ok {
				if subs, _ := cmd.Flags().GetStringSlice("subscriptions"); len(subs) > 0 {
					so.SetSubscriptionOverride(subs)
				}
			}
			if gs, ok := s.(providers.GlobalsSkipper); ok {
				if skip, _ := cmd.Flags().GetBool("skip-globals"); skip {
					gs.SetSkipGlobals(true)
				}
			}
			if cc, ok := s.(providers.CredentialConfigOverrider); ok {
				if path, _ := cmd.Flags().GetString("credential-config"); path != "" {
					cc.SetCredentialConfigOverride(path)
				}
			}
			return runScan(cmd, []providers.Scanner{s})
		}
		scanCmd.AddCommand(subcmd)
	}
	rootCmd.AddCommand(scanCmd)
}

// scanProviderLong returns the per-provider Long description shown by
// `disco scan <provider> --help`. The text lives on each provider (via the
// providers.LongDescriber capability) so the top of every help page documents
// that provider's own scoping ground rules; providers that don't implement it
// fall through to a generic blurb so adding a new scanner package never breaks
// the help.
func scanProviderLong(s providers.Scanner) string {
	if d, ok := s.(providers.LongDescriber); ok {
		return d.LongDescription()
	}
	return fmt.Sprintf("Scan %s resources.", s.Name())
}

// serviceFilterExample returns a representative pair of service prefixes for the
// given provider, used in --services flag help. The pair lives on each provider
// (via providers.ServiceFilterExemplar); unknown providers fall back to a
// generic placeholder.
func serviceFilterExample(s providers.Scanner) string {
	if e, ok := s.(providers.ServiceFilterExemplar); ok {
		return e.ServiceFilterExample()
	}
	return s.Name() + ":<service>"
}
