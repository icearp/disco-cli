package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/spf13/cobra"
)

// coverageCmd is the parent of the three drift-detection subcommands.
// Bare `disco coverage` prints help.
var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Detect drift in services / regions / resolvers (pick a subcommand)",
	Long: `Drift detection across disco's static capabilities and the cloud's live API.

Compares what disco knows how to scan — scanner emits, RegionNames lists,
resolver EdgeDecls — against each cloud's authoritative source so newly-
launched resource types, regions, or unannotated resolvers surface before
they show up as silent gaps in scan output.

This is scan-capability coverage (disco vs. the cloud's catalog). For tag-
governance coverage — the fraction of already-discovered resources carrying
each tag — see 'disco tag-coverage'.`,
	Args: cobra.NoArgs,
	Run: func(c *cobra.Command, _ []string) {
		_ = c.Help()
	},
}

var coverageServicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Measure scanner coverage of each cloud's SDK-listable resources",
	Long: `Measures how much of each cloud's listable resource surface disco scans.

The denominator is derived offline from the providers' own SDK sources
(populate the cache once with 'disco coverage sdk fetch'): every List
operation becomes a candidate resource, classified as resource, attribute
(a detail read of a parent), catalog (read-only reference data) or
non-resource. The numerator is a static pairing of each scanner's SDK calls
with the types it stores, derived from disco's own source (--source-root).

Bucket model:
  - covered      a resource candidate some scanner lists.
  - uncovered    a resource candidate no scanner lists — the gap list.
  - attribute    a detail read; listed, not counted.
  - excluded     catalog / non-resource / preview-only; listed, not counted.
  - disco-only   an emitted type with no candidate; the reason says whether
                 it is explained (no SDK, built from a non-list op, SDK skew).
  - registry-drift  only with --cross-check: the live registry (CloudFormation
                 + Service Reference, ARM Providers, Discovery) and the SDK
                 universe disagree.

Percent = covered / (covered + uncovered), computed before --filter, per
provider, per service and per depth. Pins are printed with every report:
numbers compare only across identical pins.`,
	Example: `  disco coverage services
  disco coverage services --providers gcp -o markdown
  disco coverage services --providers aws --filter gaps
  disco coverage services --providers aws --services ec2,s3 -o json | jq '.[0].summary'
  disco coverage services --cross-check --providers azure --filter registry-drift
  disco coverage services --check-strict`,
	Args: cobra.NoArgs,
	RunE: runCoverageServices,
}

var coverageRegionsCmd = &cobra.Command{
	Use:   "regions",
	Short: "Diff each provider's static region list against the cloud's live SDK regions",
	Long: `Compares each provider's compiled-in RegionNames slice (the disco-side
opinion of "what could be scanned") against the cloud's authoritative
SDK region/location list:

  - AWS:    ec2:DescribeRegions (filtered to opt-in-not-required + opted-in)
  - Azure:  armsubscription.Subscriptions.ListLocations(subscriptionId)
  - GCP:    compute.Regions.List(projectId)

Status values:
  covered  region appears in both static list and live API
  stale    static list has it but live API doesn't (region retired or typo)
  missing  live API has it but static list doesn't — refresh
           internal/providers/<p>/regions.go`,
	Example: `  disco coverage regions
  disco coverage regions --providers aws --check-strict
  disco coverage regions --providers azure --regions eu-central-2`,
	Args: cobra.NoArgs,
	RunE: runCoverageRegions,
}

var coverageResolversCmd = &cobra.Command{
	Use:   "resolvers",
	Short: "List resolvers with their edge annotations, or orphan types (--missing)",
	Long: `Default mode: list every registered AWS resolver and its declared
EdgeDecl count. Unannotated resolvers (count=0) surface as sweep
targets — either deliberate no-ops (sidecar populators, audit-stubs)
or drift signal that hasn't been triaged.

--missing flips the output to the orphan-type inventory: every emitted
disco type that never appears as the Source of a declared EdgeDecl.
Candidate gap list for new resolvers.

--services filters to resolvers (or orphan types) whose service segment
matches one of the named services.

Implemented by AWS, Azure, and GCP. --providers selects which (unset = all that
support resolver auditing); naming a provider without auditing support errors.`,
	Example: `  disco coverage resolvers
  disco coverage resolvers --providers azure
  disco coverage resolvers --only-unannotated
  disco coverage resolvers --missing
  disco coverage resolvers --services ec2,s3
  disco coverage resolvers --missing --services ec2 -o json`,
	Args: cobra.NoArgs,
	RunE: runCoverageResolvers,
}

func init() {
	// Parent owns --output (PersistentFlags): one declaration, inherited by
	// every subcommand.
	coverageCmd.PersistentFlags().StringP("output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = coverageCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))

	// services subcommand flags.
	coverageServicesCmd.Flags().StringSlice("providers", nil, fmt.Sprintf("Limit to listed providers (%s); empty = all registered", providerListHint()))
	coverageServicesCmd.Flags().String("sdk-cache", sdkinv.DefaultCacheRoot(), "SDK source cache directory (see 'disco coverage sdk fetch')")
	coverageServicesCmd.Flags().String("source-root", defaultSourceRoot(), "disco source checkout for scanner pairing; empty = name matching only")
	coverageServicesCmd.Flags().Bool("cross-check", false, "Also diff the SDK universe against the live upstream registry (needs credentials)")
	coverageServicesCmd.Flags().StringSlice("regions", nil, "--cross-check only: regions for the CloudFormation registry call (union); empty = us-east-1")
	coverageServicesCmd.Flags().String("profile", "", "--cross-check only: AWS profile name")
	coverageServicesCmd.Flags().StringSlice("subscriptions", nil, "--cross-check only: Azure subscription ID(s); first is used, empty = autodetect")
	coverageServicesCmd.Flags().String("filter", "all", "Filter rows: "+strings.Join(coverage.Filters, ", ")+" (gaps = uncovered + unexplained disco-only)")
	_ = coverageServicesCmd.RegisterFlagCompletionFunc("filter", staticCompletion(coverage.Filters...))
	coverageServicesCmd.Flags().StringSlice("services", nil, "Limit rows to listed services (matched against the row's service segment)")
	coverageServicesCmd.Flags().Duration("timeout", 3*time.Minute, "--cross-check only: per-provider live-fetch timeout (GCP walks every Discovery doc)")
	coverageServicesCmd.Flags().Bool("check-strict", false, "Exit 1 on unexplained disco-only rows. A missing SDK cache or failed registry fetch always exits 2.")

	// regions subcommand flags.
	coverageRegionsCmd.Flags().StringSlice("providers", nil, fmt.Sprintf("Limit to listed providers (%s); empty = all registered", providerListHint()))
	coverageRegionsCmd.Flags().StringSlice("regions", nil, "Filter diff output to listed regions; empty = no filter")
	coverageRegionsCmd.Flags().String("profile", "", "AWS profile name (--providers aws only)")
	coverageRegionsCmd.Flags().StringSlice("subscriptions", nil, "Azure subscription ID(s) for the registry context (--providers azure only); first is used, empty = autodetect")
	coverageRegionsCmd.Flags().Duration("timeout", 60*time.Second, "Per-provider live-fetch timeout")
	coverageRegionsCmd.Flags().Bool("check-strict", false, "Exit 1 on any non-covered row (drift). A region-list fetch failure always exits 2, with or without this flag.")

	// resolvers subcommand flags.
	coverageResolversCmd.Flags().StringSlice("providers", nil, "Limit to listed providers (aws only today); empty = aws")
	coverageResolversCmd.Flags().StringSlice("services", nil, "Filter to resolvers (or orphan types) touching the listed services")
	coverageResolversCmd.Flags().Bool("only-unannotated", false, "List mode only: omit resolvers that already declare ≥1 EdgeDecl")
	coverageResolversCmd.Flags().Bool("missing", false, "Switch to orphan-type mode: emit disco types never appearing as EdgeDecl.Source")

	coverageCmd.AddCommand(coverageServicesCmd, coverageRegionsCmd, coverageResolversCmd)
	rootCmd.AddCommand(coverageCmd)
}

// errCoverageRegistryUnreachable signals a provider's upstream registry fetch
// failed (e.g. expired/missing credentials), so --cross-check can't be
// assessed. Mapped to exit 2 in Execute() (vs exit 1 for genuine drift) so CI
// can tell transient credential failure from real drift.
var errCoverageRegistryUnreachable = errors.New("upstream registry unreachable for")

// errCoverageInventoryUnavailable signals the SDK source cache lacks a
// provider's pinned snapshot, so the denominator can't be derived. Exit 2,
// like a registry failure: nothing about disco's coverage changed.
var errCoverageInventoryUnavailable = errors.New("sdk inventory unavailable")

// defaultSourceRoot is the working directory when it is a disco checkout
// (go.mod names this module); pairing needs the scanner source, and any
// other directory would be a different program.
func defaultSourceRoot() string {
	raw, err := os.ReadFile("go.mod")
	if err != nil || !strings.HasPrefix(string(raw), "module github.com/icearp/disco-cli\n") {
		return ""
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// outputFormat resolves the --output persistent flag on the invoking subcommand.
func outputFormat(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("output")
	return v
}

// firstOrEmpty returns the first element of vals, or "" if empty. Shaped to
// consume a pflag GetStringSlice (slice, error) result directly, for flags
// that are plural for consistency but take only one meaningful value (e.g.
// Azure's subscription-invariant coverage registry context).
func firstOrEmpty(vals []string, _ error) string {
	if len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// servicesOptions are the parsed flags of `coverage services`.
type servicesOptions struct {
	providers   []string
	cache       sdkinv.Cache
	sourceRoot  string
	crossCheck  bool
	fetch       coverage.FetchOptions
	timeout     time.Duration
	filter      string
	services    []string
	checkStrict bool
}

func runCoverageServices(cmd *cobra.Command, _ []string) (rerr error) {
	outputFmt := outputFormat(cmd)
	defer func() { maybeStructuredError(outputFmt, rerr) }()
	switch outputFmt {
	case "markdown", "md", "table", "json", "jsonl", "csv":
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", outputFmt)
	}
	var o servicesOptions
	o.providers, _ = cmd.Flags().GetStringSlice("providers")
	root, _ := cmd.Flags().GetString("sdk-cache")
	o.cache = sdkinv.Cache{Root: root}
	o.sourceRoot, _ = cmd.Flags().GetString("source-root")
	o.crossCheck, _ = cmd.Flags().GetBool("cross-check")
	o.fetch.Regions, _ = cmd.Flags().GetStringSlice("regions")
	o.fetch.Profile, _ = cmd.Flags().GetString("profile")
	o.fetch.Subscription = firstOrEmpty(cmd.Flags().GetStringSlice("subscriptions"))
	o.timeout, _ = cmd.Flags().GetDuration("timeout")
	o.filter, _ = cmd.Flags().GetString("filter")
	o.services, _ = cmd.Flags().GetStringSlice("services")
	o.checkStrict, _ = cmd.Flags().GetBool("check-strict")
	if !slices.Contains(coverage.Filters, o.filter) {
		return fmt.Errorf("--filter must be one of %s; got %q", strings.Join(coverage.Filters, "|"), o.filter)
	}
	if o.filter == "registry-drift" && !o.crossCheck {
		return fmt.Errorf("--filter registry-drift needs --cross-check")
	}

	matrices, err := buildServiceMatrices(cmd.Context(), o)
	if err != nil {
		return err
	}
	if err := renderServiceMatrices(cmd.OutOrStdout(), outputFmt, matrices); err != nil {
		return err
	}
	if o.checkStrict {
		for _, m := range matrices {
			if m.Summary.Unexplained > 0 {
				return fmt.Errorf("%s: %d emitted types pair with no SDK call (--check-strict); see --filter disco-only", m.Provider, m.Summary.Unexplained)
			}
		}
	}
	return nil
}

// buildServiceMatrices derives one matrix per provider from the SDK cache,
// pairs it with the scanner source when available, and applies the row
// filter after the summaries are computed.
func buildServiceMatrices(ctx context.Context, o servicesOptions) ([]coverage.Matrix, error) {
	covProviders, err := resolveCoverageProviders(o.providers)
	if err != nil {
		return nil, err
	}
	if len(covProviders) == 0 {
		return nil, fmt.Errorf("no coverage providers registered")
	}
	var matrices []coverage.Matrix
	var fetchFailures []string
	for _, p := range covProviders {
		in, err := inventoryInputs(ctx, o, p)
		if err != nil {
			return nil, err
		}
		m := coverage.BuildInventory(in)
		if o.crossCheck {
			cc, ok := p.(coverage.CrossChecker)
			if !ok {
				fmt.Fprintf(os.Stderr, "  %s: no registry cross-check support; skipping\n", p.Name())
			} else {
				fetchCtx, cancel := context.WithTimeout(ctx, o.timeout)
				registry, err := cc.CrossCheck(fetchCtx, o.fetch)
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "  %s: registry fetch failed: %v\n", p.Name(), err)
					fetchFailures = append(fetchFailures, p.Name())
					continue
				}
				coverage.CrossCheck(&m, in.Universe, registry, cc)
			}
		}
		m.Rows = coverage.Filter(m.Rows, o.filter, o.services)
		matrices = append(matrices, m)
	}
	// Fetch failure is always fatal (exit 2), independent of --check-strict;
	// returned before rendering so no misleading matrix is emitted.
	if len(fetchFailures) > 0 {
		return nil, fmt.Errorf("%w: %s; check credentials, then retry or scope --providers", errCoverageRegistryUnreachable, strings.Join(fetchFailures, ", "))
	}
	return matrices, nil
}

// inventoryInputs extracts the provider's universe from the cache and, when
// the source root holds the scanner package, its pairings.
func inventoryInputs(ctx context.Context, o servicesOptions, p coverage.Provider) (coverage.Inputs, error) {
	if verbose {
		fmt.Fprintf(os.Stderr, "Extracting %s SDK inventory...\n", p.Name())
	}
	scannerDir := ""
	if o.sourceRoot == "" {
		fmt.Fprintf(os.Stderr, "  %s: no --source-root; pairing unavailable, matching candidates by name only\n", p.Name())
	} else {
		scannerDir = filepath.Join(o.sourceRoot, "internal", "providers", p.Name())
	}
	in, err := coverage.InputsFromCache(ctx, o.cache, p.Name(), p.Emits(), scannerDir)
	if err != nil && in.Universe == nil {
		return in, fmt.Errorf("%w: %v", errCoverageInventoryUnavailable, err)
	}
	// A pairing failure with the universe in hand (unparsable scanner source,
	// wrong --source-root) stays fatal: degrading to name matching would
	// print a lower percentage that looks like a real regression.
	return in, err
}

func renderServiceMatrices(w io.Writer, outputFmt string, matrices []coverage.Matrix) error {
	switch outputFmt {
	case "json":
		return coverage.RenderJSON(w, matrices)
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, m := range matrices {
			for _, r := range m.Rows {
				if err := enc.Encode(r); err != nil {
					return err
				}
			}
		}
		return nil
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"provider", "service", "key", "disco_type", "bucket", "depth", "parent", "scope", "reason", "ops"})
		for _, m := range matrices {
			for _, r := range m.Rows {
				_ = cw.Write([]string{r.Provider, r.Service, r.Key, r.DiscoType, string(r.Bucket), strconv.Itoa(r.Depth), r.Parent, r.Scope, r.Reason, strings.Join(r.Ops, " ")})
			}
		}
		cw.Flush()
		return cw.Error()
	case "markdown", "md":
		return coverage.RenderMarkdown(w, matrices)
	default:
		return coverage.RenderTable(w, matrices)
	}
}

func runCoverageRegions(cmd *cobra.Command, _ []string) (rerr error) {
	provNames, _ := cmd.Flags().GetStringSlice("providers")
	regionFilter, _ := cmd.Flags().GetStringSlice("regions")
	profile, _ := cmd.Flags().GetString("profile")
	subscription := firstOrEmpty(cmd.Flags().GetStringSlice("subscriptions"))
	timeout, _ := cmd.Flags().GetDuration("timeout")
	checkStrict, _ := cmd.Flags().GetBool("check-strict")
	outputFmt := outputFormat(cmd)
	defer func() { maybeStructuredError(outputFmt, rerr) }()

	switch outputFmt {
	case "table", "markdown", "md", "json", "jsonl", "csv":
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", outputFmt)
	}

	covProviders, err := resolveCoverageProviders(provNames)
	if err != nil {
		return err
	}

	opts := coverage.FetchOptions{Profile: profile, Subscription: subscription}

	var rows []coverage.RegionRow
	var fetchFailures []string
	for _, p := range covProviders {
		rl, ok := p.(coverage.RegionLister)
		if !ok {
			fmt.Fprintf(os.Stderr, "  %s: no RegionLister support; skipping\n", p.Name())
			continue
		}
		scanner, ok := providers.Get(p.Name())
		if !ok {
			fmt.Fprintf(os.Stderr, "  %s: scanner not registered; skipping\n", p.Name())
			continue
		}
		rn, ok := scanner.(providers.RegionNamer)
		if !ok {
			fmt.Fprintf(os.Stderr, "  %s: scanner is not RegionNamer; skipping\n", p.Name())
			continue
		}
		if verbose {
			fmt.Fprintf(os.Stderr, "Fetching %s region list...\n", p.Name())
		}
		fetchCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
		live, err := rl.FetchRegions(fetchCtx, opts)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: fetch failed: %v\n", p.Name(), err)
			fetchFailures = append(fetchFailures, p.Name())
			continue
		}
		diff := coverage.DiffRegions(rn.RegionNames(), live)
		for i := range diff {
			diff[i].Provider = p.Name()
		}
		rows = append(rows, diff...)
	}

	// Fetch failure is always fatal (exit 2), independent of --check-strict.
	if len(fetchFailures) > 0 {
		return fmt.Errorf("%w: %s; check credentials, then retry or scope --providers", errCoverageRegistryUnreachable, strings.Join(fetchFailures, ", "))
	}

	if len(regionFilter) > 0 {
		allow := map[string]struct{}{}
		for _, r := range regionFilter {
			allow[r] = struct{}{}
		}
		filtered := rows[:0]
		for _, r := range rows {
			if _, ok := allow[r.Region]; ok {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	w := cmd.OutOrStdout()
	switch outputFmt {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return err
		}
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"provider", "region", "status"})
		for _, r := range rows {
			_ = cw.Write([]string{r.Provider, r.Region, r.Status})
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
	case "markdown", "md":
		if err := coverage.RenderRegionsMarkdown(w, rows); err != nil {
			return err
		}
	default:
		if err := coverage.RenderRegionsTable(w, rows); err != nil {
			return err
		}
	}

	if checkStrict {
		for _, r := range rows {
			if r.Status != coverage.RegionCovered {
				return fmt.Errorf("region drift present (--check-strict): %s/%s = %s", r.Provider, r.Region, r.Status)
			}
		}
	}
	return nil
}

func runCoverageResolvers(cmd *cobra.Command, _ []string) (rerr error) {
	provNames, _ := cmd.Flags().GetStringSlice("providers")
	services, _ := cmd.Flags().GetStringSlice("services")
	onlyUnannotated, _ := cmd.Flags().GetBool("only-unannotated")
	missing, _ := cmd.Flags().GetBool("missing")
	outputFmt := outputFormat(cmd)
	defer func() { maybeStructuredError(outputFmt, rerr) }()

	// Pre-validate like the services/regions siblings; an unknown value would
	// otherwise fall through silently to the tabwriter table renderer.
	switch outputFmt {
	case "table", "markdown", "md", "json", "jsonl", "csv":
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", outputFmt)
	}

	auditors, err := selectedAuditors(provNames)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	if missing {
		return runResolversMissing(w, auditors, services, outputFmt)
	}
	return runResolversList(w, auditors, services, onlyUnannotated, outputFmt)
}

// auditorPair binds a coverage provider to its resolver-auditing view.
type auditorPair struct {
	prov coverage.Provider
	ra   coverage.ResolverAuditor
}

// selectedAuditors resolves the providers whose resolver registries get
// audited. Empty provNames (the `--providers` default) selects every compiled
// provider implementing coverage.ResolverAuditor; a non-empty list selects
// exactly those, erroring if a named provider is unknown or lacks resolver
// auditing. Errors clearly when no auditor exists (e.g. a slim build
// excluding AWS+Azure) so `coverage resolvers` degrades gracefully.
func selectedAuditors(provNames []string) ([]auditorPair, error) {
	var out []auditorPair
	if len(provNames) == 0 {
		for _, prov := range coverage.All() {
			if ra, ok := prov.(coverage.ResolverAuditor); ok {
				out = append(out, auditorPair{prov, ra})
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no provider in this build supports resolver coverage")
		}
		return out, nil
	}
	for _, p := range provNames {
		prov, ok := coverage.Get(p)
		if !ok {
			return nil, fmt.Errorf("provider %q has no coverage support; registered: %v", p, coverage.Names())
		}
		ra, ok := prov.(coverage.ResolverAuditor)
		if !ok {
			return nil, fmt.Errorf("provider %q does not support resolver coverage", p)
		}
		out = append(out, auditorPair{prov, ra})
	}
	return out, nil
}

// runResolversList prints per-resolver EdgeDecl counts, optionally filtered
// to resolvers touching a named service.
func runResolversList(w io.Writer, auditors []auditorPair, services []string, onlyUnannotated bool, outputFmt string) error {
	allowed := lowerSet(services)
	type row struct {
		Provider string   `json:"provider"`
		Resolver string   `json:"resolver"`
		Edges    int      `json:"edges"`
		Services []string `json:"services,omitempty"`
	}
	var rows []row
	total, annotated, unannotated := 0, 0, 0
	for _, a := range auditors {
		provName := a.prov.Name()
		infos := a.ra.ListResolvers()
		total += len(infos)
		for _, r := range infos {
			if len(allowed) > 0 && !anyServiceMatch(r.Services, allowed) {
				continue
			}
			if r.EdgeCount == 0 {
				unannotated++
				rows = append(rows, row{Provider: provName, Resolver: r.Name, Edges: 0, Services: r.Services})
				continue
			}
			annotated++
			if onlyUnannotated {
				continue
			}
			rows = append(rows, row{Provider: provName, Resolver: r.Name, Edges: r.EdgeCount, Services: r.Services})
		}
	}
	switch outputFmt {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return err
		}
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"provider", "resolver", "edges", "services"})
		for _, r := range rows {
			_ = cw.Write([]string{r.Provider, r.Resolver, strconv.Itoa(r.Edges), strings.Join(r.Services, ",")})
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
	case "markdown", "md":
		mdRows := make([][]string, 0, len(rows))
		for _, r := range rows {
			mdRows = append(mdRows, []string{r.Provider, r.Resolver, strconv.Itoa(r.Edges), strings.Join(r.Services, ",")})
		}
		if err := renderMarkdownTable(w, []string{"Provider", "Resolver", "Edges", "Services"}, mdRows); err != nil {
			return err
		}
	default:
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "PROVIDER\tRESOLVER\tEDGES\tSERVICES")
		for _, r := range rows {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", r.Provider, r.Resolver, r.Edges, strings.Join(r.Services, ","))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "\n%d resolvers total — %d annotated, %d unannotated\n", total, annotated, unannotated)
	return nil
}

// runResolversMissing prints orphan disco types — those never appearing as
// the Source of any EdgeDecl — optionally filtered to types whose service
// segment matches a named service.
func runResolversMissing(w io.Writer, auditors []auditorPair, services []string, outputFmt string) error {
	allowed := lowerSet(services)
	type row struct {
		Provider  string `json:"provider"`
		DiscoType string `json:"discoType"`
		Service   string `json:"service"`
	}
	var rows []row
	totalEmitted := 0
	for _, a := range auditors {
		provName := a.prov.Name()
		emitted := make(map[string]struct{})
		for _, decl := range a.prov.Emits() {
			if decl.Leaf {
				continue
			}
			emitted[decl.DiscoType] = struct{}{}
		}
		totalEmitted += len(emitted)
		sources := make(map[string]struct{})
		for _, s := range a.ra.ResolverEdgeSources() {
			sources[s] = struct{}{}
		}
		orphans := make([]string, 0, len(emitted))
		for t := range emitted {
			if _, has := sources[t]; has {
				continue
			}
			orphans = append(orphans, t)
		}
		sort.Strings(orphans)
		for _, t := range orphans {
			svc := discoServiceSegment(t)
			if len(allowed) > 0 && !allowed[strings.ToLower(svc)] {
				continue
			}
			rows = append(rows, row{Provider: provName, DiscoType: t, Service: svc})
		}
	}
	switch outputFmt {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return err
		}
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"provider", "disco_type", "service"})
		for _, r := range rows {
			_ = cw.Write([]string{r.Provider, r.DiscoType, r.Service})
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
	case "markdown", "md":
		mdRows := make([][]string, 0, len(rows))
		for _, r := range rows {
			mdRows = append(mdRows, []string{r.Provider, r.DiscoType, r.Service})
		}
		if err := renderMarkdownTable(w, []string{"Provider", "Disco Type", "Service"}, mdRows); err != nil {
			return err
		}
	default:
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "PROVIDER\tDISCO_TYPE\tSERVICE")
		for _, r := range rows {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Provider, r.DiscoType, r.Service)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "\n%d source-orphan types out of %d emitted\n", len(rows), totalEmitted)
	return nil
}

// resolveCoverageProviders maps the --providers slice to the coverage.Provider
// list. Empty slice = all registered. Unknown name = error listing the
// registered set.
func resolveCoverageProviders(names []string) ([]coverage.Provider, error) {
	if len(names) == 0 {
		return coverage.All(), nil
	}
	out := make([]coverage.Provider, 0, len(names))
	for _, n := range names {
		p, ok := coverage.Get(n)
		if !ok {
			return nil, fmt.Errorf("provider %q has no coverage support; registered: %v", n, coverage.Names())
		}
		out = append(out, p)
	}
	return out, nil
}

func discoServiceSegment(t string) string {
	parts := strings.SplitN(t, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

func lowerSet(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[strings.ToLower(s)] = true
	}
	return out
}

func anyServiceMatch(svcs []string, allowed map[string]bool) bool {
	for _, s := range svcs {
		if allowed[strings.ToLower(s)] {
			return true
		}
	}
	return false
}
