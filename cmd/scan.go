package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/scanrun"
	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
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
but only re-scan if 6 h have elapsed.

Examples:
  disco scan aws
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
				return fmt.Errorf("unknown provider %q (available: %s)", name, strings.Join(providers.Names(), ", "))
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
// checkpoint table to carry per-service watermarks the paid incremental
// scanner consumes; the OSS path persists fresh checkpoints from this scan_id
// without consuming them.
func startOrResumeScan(db *store.Store, resumeFlag string, providers []string, scope map[string]any) (string, bool, error) {
	if resumeFlag == "" {
		if scope == nil {
			scope = map[string]any{"providers": providers}
		}
		id, err := db.CreateScan(providers, scope)
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
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		return runScanDryRun(cmd, names)
	}

	// openWriteDB prefers the paid hook (Postgres in SaaS deployments
	// when DISCO_PG_DSN + DISCO_TENANT_ID + DISCO_PG_SCHEMA are set);
	// falls back to SQLite at defaultDBPath() for OSS / local-dev.
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
	// Without it (default), a fresh scan_id is generated. The actual
	// per-page resume hook is consumed by the paid incremental scanner;
	// the OSS path persists checkpoints and exposes the lookup so users
	// can swap to the paid feature without re-scanning.
	resumeFlag, _ := cmd.Flags().GetString("resume")
	scope := buildScanScope(cmd, names, scanners)
	scanID, resuming, err := startOrResumeScan(db, resumeFlag, names, scope)
	if err != nil {
		return err
	}
	start := time.Now()
	if resuming {
		cps, lerr := db.ListCheckpoints(scanID)
		if lerr == nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"Resuming scan %s with %d checkpoint(s)\n", scanID, len(cps))
		}
	}

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

	scopeWidth := scopeColumnWidth(scanners)

	// Print a status line each time a provider completes scanning one service.
	// scope = the per-call dimension that would otherwise produce duplicate
	// lines in multi-region / multi-account scans (AWS region or "global",
	// Azure subscription ID, GCP project ID). Without it, --regions us-east-1,
	// eu-west-1 prints aws:ec2 twice with no way to tell them apart.
	// Accumulate totals here — these are the source of truth for the summary counts.
	// errCount > 0 means the service hit one or more errors (which were already
	// reported via OnError); annotate the line with "(with errors)" so the user
	// can scan output for trouble without grepping.
	var totalSeen, totalNew int64
	db.OnServiceComplete = func(service, scope string, total, inserted, errCount int, disabled bool) {
		atomic.AddInt64(&totalSeen, int64(total))
		atomic.AddInt64(&totalNew, int64(inserted))
		if quiet {
			return
		}
		suffix := ""
		switch {
		case disabled:
			suffix = "  (service disabled)"
		case errCount > 0:
			suffix = "  (with errors)"
		}
		_, _ = fmt.Fprintf(progressW, "  [%s] %-*s  %-*s  (%d total, %d new)%s\n",
			time.Since(start).Round(time.Second), nameWidth, service, scopeWidth, scope, total, inserted, suffix)
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

	// Fan-out + warning/error capture lives in scanrun so the same code path
	// drives the API server (cmd/serve_paid.go). RunScanners chains its
	// callbacks on top of any caller-installed OnWarn/OnError; here neither
	// is set so RunScanners is the sole capture point.
	ctx := context.Background()
	warnings, scanErrors := scanrun.RunScanners(ctx, db, scanID, scanners)

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
		switch s.Name() {
		case "azure":
			if 36 > width {
				width = 36
			}
		case "gcp":
			if 30 > width {
				width = 30
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
		if legacy, _ := cmd.Flags().GetStringSlice("region"); len(regions) == 0 && len(legacy) > 0 {
			regions = legacy
		}
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

// runScanDryRun prints "would scan" / "would skip" decisions per provider
// without opening SDK clients or hitting cloud APIs. Reads the local DB
// read-only to evaluate --if-older-than against latest complete scans.
// Always exits 0 — pipeline operators are expected to read stdout to make
// scheduling decisions, not the exit code.
func runScanDryRun(cmd *cobra.Command, names []string) error {
	d, _ := cmd.Flags().GetDuration("if-older-than")
	db, err := store.OpenReadOnly(defaultDBPath())
	if err != nil {
		// First-run UX: report decisions even without a DB. Recency gate
		// trivially passes (no prior scan ⇒ scan must run).
		for _, name := range names {
			detail := "no recency gate"
			if d > 0 {
				detail = fmt.Sprintf("threshold %s, no DB on disk yet", d)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "would scan: %s (%s)\n", name, detail)
		}
		return nil
	}
	defer func() { _ = db.Close() }()
	for _, name := range names {
		decision := "would scan"
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
						decision = "would skip"
						detail = fmt.Sprintf("latest=%s ago < %s", age, d)
					} else {
						detail = fmt.Sprintf("latest=%s ago >= %s", age, d)
					}
				}
			}
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", decision, name, detail)
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

func init() {
	scanCmd.Flags().StringSlice("providers", nil, "comma-separated provider(s) to scan (e.g. aws,gcp); omit to scan all")
	// Persistent so subcommands (disco scan aws, etc.) inherit the flag.
	scanCmd.PersistentFlags().Bool("quiet", false, "suppress per-service progress output; only print the final summary")
	scanCmd.PersistentFlags().String("resume", "", "resume a previous scan: pass a scan ID, or 'latest' to pick the most recent incomplete scan")
	scanCmd.PersistentFlags().Duration("if-older-than", 0,
		"skip the scan (exit 0) when the latest complete scan for every targeted provider is younger than this duration (e.g. 1h, 24h)")
	scanCmd.PersistentFlags().Bool("dry-run", false,
		"resolve provider selection + --if-older-than and print 'would scan / would skip' decisions; no SDK clients constructed, no cloud APIs called")

	// Add one subcommand per registered provider so users can run e.g. "disco scan aws".
	// providers.All() is populated by init()s in cmd/providers.go's blank imports,
	// which are guaranteed to run before this init().
	for _, s := range providers.All() {
		s := s
		subcmd := &cobra.Command{
			Use:   s.Name(),
			Short: fmt.Sprintf("Scan %s resources", s.Name()),
			Long:  scanProviderLong(s.Name()),
		}
		// Register optional flags only when the provider implements the matching
		// capability interface — keeps --help honest (no flags listed that would
		// be silently ignored).
		if _, ok := s.(providers.ServiceFilterer); ok {
			example := serviceFilterExample(s.Name())
			subcmd.Flags().StringSlice("services", nil,
				fmt.Sprintf("comma-separated %s services to scan (e.g. %s); omit to scan all", s.Name(), example))
		}
		if _, ok := s.(providers.RegionOverrider); ok {
			subcmd.Flags().StringSlice("regions", nil,
				"regions to scan, comma-separated (overrides config; e.g. us-west-2,eu-west-1)")
			subcmd.Flags().StringSlice("region", nil, "alias for --regions")
			_ = subcmd.Flags().MarkDeprecated("region", "use --regions instead")
		}
		if _, ok := s.(providers.ProfileOverrider); ok {
			subcmd.Flags().String("profile", "",
				"named credential profile (e.g. a profile defined in ~/.aws/config)")
		}
		if _, ok := s.(providers.RoleOverrider); ok {
			subcmd.Flags().String("role-arn", "",
				"AssumeRole target — pins the scan to a single account reached by assuming this role; bypasses the config file's accounts: section")
			subcmd.Flags().String("external-id", "",
				"STS ExternalId for --role-arn (only honoured when --role-arn is also set)")
		}
		if _, ok := s.(providers.GlobalsSkipper); ok {
			subcmd.Flags().Bool("skip-globals", false,
				"skip services whose scope is account-wide (e.g. AWS IAM, Route53, CloudFront); regional services unaffected")
		}
		subcmd.RunE = func(cmd *cobra.Command, _ []string) error {
			if sf, ok := s.(providers.ServiceFilterer); ok {
				if svcs, _ := cmd.Flags().GetStringSlice("services"); len(svcs) > 0 {
					sf.SetServiceFilter(svcs)
				}
			}
			if ro, ok := s.(providers.RegionOverrider); ok {
				regions, _ := cmd.Flags().GetStringSlice("regions")
				if legacy, _ := cmd.Flags().GetStringSlice("region"); len(regions) == 0 && len(legacy) > 0 {
					regions = legacy
				}
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
			if gs, ok := s.(providers.GlobalsSkipper); ok {
				if skip, _ := cmd.Flags().GetBool("skip-globals"); skip {
					gs.SetSkipGlobals(true)
				}
			}
			return runScan(cmd, []providers.Scanner{s})
		}
		scanCmd.AddCommand(subcmd)
	}
	rootCmd.AddCommand(scanCmd)
}

// serviceFilterExample returns a representative pair of service prefixes
// for the given provider, used in --services flag help. Falls back to a
// generic placeholder for unknown providers.
// scanProviderLong returns the per-provider Long description shown by
// `disco scan <provider> --help`. Centralised so the top of every help
// page documents the same ground rules: how scoping works, what flag is
// safe to omit, and the canonical one-liner example. Unknown providers
// fall through to a generic blurb so adding a new scanner package never
// breaks the help.
func scanProviderLong(provider string) string {
	switch provider {
	case "aws":
		return `Scan AWS resources across one or more regions.

Account scope comes from the ambient AWS identity (env vars, instance
profile, ~/.aws/config) or, if config.yaml lists explicit accounts, the
declared role-chain per entry. Use --profile to pick a named profile and
--regions to override the configured region list. --skip-globals omits
account-wide services (IAM, Route53, CloudFront, etc.) when running a
per-region audit.

Examples:
  disco scan aws
  disco scan aws --regions us-west-2,eu-west-1
  disco scan aws --services aws:ec2,aws:s3 --profile prod
  disco scan aws --skip-globals --regions us-east-1`
	case "azure":
		return `Scan Azure resources across reachable subscriptions.

Subscription scope comes from DefaultAzureCredential (az login, managed
identity, env vars) or the explicit 'subscriptions:' list in config.yaml.
There is no --regions / --profile flag — Azure scopes per
subscription/resource group, configured statically. --services narrows
the scanner set when iterating on one provider.

Examples:
  disco scan azure
  disco scan azure --services azure:compute,azure:network`
	case "gcp":
		return `Scan GCP resources across reachable projects.

Project scope comes from Application Default Credentials (gcloud auth
application-default login) or the explicit 'projects:' list in
config.yaml. There is no --regions / --profile flag — GCP fans out per
project against each service's default scope. --services narrows the
scanner set when iterating on one provider.

Examples:
  disco scan gcp
  disco scan gcp --services gcp:compute,gcp:storage`
	default:
		return fmt.Sprintf("Scan %s resources.", provider)
	}
}

func serviceFilterExample(provider string) string {
	switch provider {
	case "aws":
		return "aws:ec2,aws:s3"
	case "azure":
		return "azure:compute,azure:network"
	case "gcp":
		return "gcp:compute,gcp:storage"
	default:
		return provider + ":<service>"
	}
}
