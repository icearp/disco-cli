package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/providers"
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
they show up as silent gaps in scan output.`,
	Args: cobra.NoArgs,
	Run: func(c *cobra.Command, _ []string) {
		_ = c.Help()
	},
}

var coverageServicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Diff scanner emits against the upstream type registry",
	Long: `Compares disco's registered scanners against the live upstream type
registry of each cloud provider:

  - AWS:    CloudFormation ListTypes (Public, Resource)
  - Azure:  ARM Providers/List?$expand=resourceTypes
  - GCP:    Discovery API (https://www.googleapis.com/discovery/v1/apis)

Coverage truth source is the per-scanner emits []TypeDecl declared on each
serviceEntry — disco knows what each scanner upserts, not a static slice
that may have drifted.

Bucket model:
  - covered          disco scanner + upstream registry entry both present.
  - uncovered        upstream registry entry has no disco scanner.
  - synthetic        disco-only type (no upstream registry entry expected).
  - upstream-missing disco emits but upstream registry no longer publishes
                     — drift signal. Surface via --check-strict for CI gating.`,
	Example: `  disco coverage services
  disco coverage services --providers gcp
  disco coverage services --providers aws --filter uncovered
  disco coverage services --providers aws --regions us-east-1,us-west-2
  disco coverage services -o json | jq '.[].rows[] | select(.bucket=="upstream-missing")'
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

Implemented by AWS and Azure. --providers selects which (unset = all that
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
	// Parent owns --output (PersistentFlags) so every subcommand inherits the
	// same set of formats with one declaration.
	coverageCmd.PersistentFlags().StringP("output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = coverageCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))

	// services subcommand flags.
	coverageServicesCmd.Flags().StringSlice("providers", nil, fmt.Sprintf("Limit to listed providers (%s); empty = all registered", providerListHint()))
	coverageServicesCmd.Flags().StringSlice("regions", nil, "Regions for the upstream registry call (CFN ListTypes per region, union); empty = SDK default (us-east-1)")
	coverageServicesCmd.Flags().String("profile", "", "AWS profile name (--providers aws only)")
	coverageServicesCmd.Flags().String("subscription", "", "Azure subscription ID (--providers azure only); empty = autodetect")
	coverageServicesCmd.Flags().String("filter", "all", "Filter rows: all, covered, uncovered, synthetic, upstream-missing")
	coverageServicesCmd.Flags().StringSlice("services", nil, "Limit rows to listed services (matched against the row's service segment)")
	coverageServicesCmd.Flags().Duration("timeout", 60*time.Second, "Per-provider live-fetch timeout")
	coverageServicesCmd.Flags().Bool("check-strict", false, "Exit 1 on upstream-missing rows (drift); exit 2 on transient registry-fetch failure")

	// regions subcommand flags.
	coverageRegionsCmd.Flags().StringSlice("providers", nil, fmt.Sprintf("Limit to listed providers (%s); empty = all registered", providerListHint()))
	coverageRegionsCmd.Flags().StringSlice("regions", nil, "Filter diff output to listed regions; empty = no filter")
	coverageRegionsCmd.Flags().String("profile", "", "AWS profile name (--providers aws only)")
	coverageRegionsCmd.Flags().String("subscription", "", "Azure subscription ID (--providers azure only); empty = autodetect")
	coverageRegionsCmd.Flags().Duration("timeout", 60*time.Second, "Per-provider live-fetch timeout")
	coverageRegionsCmd.Flags().Bool("check-strict", false, "Exit 1 on any non-covered row (drift)")

	// resolvers subcommand flags.
	coverageResolversCmd.Flags().StringSlice("providers", nil, "Limit to listed providers (aws only today); empty = aws")
	coverageResolversCmd.Flags().StringSlice("services", nil, "Filter to resolvers (or orphan types) touching the listed services")
	coverageResolversCmd.Flags().Bool("only-unannotated", false, "List mode only: omit resolvers that already declare ≥1 EdgeDecl")
	coverageResolversCmd.Flags().Bool("missing", false, "Switch to orphan-type mode: emit disco types never appearing as EdgeDecl.Source")

	coverageCmd.AddCommand(coverageServicesCmd, coverageRegionsCmd, coverageResolversCmd)
	rootCmd.AddCommand(coverageCmd)
}

// errCoverageRegistryUnreachable signals --check-strict cannot assess drift
// because at least one provider's upstream registry fetch failed. Mapped to
// exit 2 in Execute() (vs exit 1 for genuine drift) so CI consumers can
// distinguish transient registry failures from real drift signal.
var errCoverageRegistryUnreachable = errors.New("cannot assess --check-strict: upstream registry unreachable for")

// outputFormat resolves the --output flag (parent persistent flag) on the
// invoking subcommand.
func outputFormat(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("output")
	return v
}

func runCoverageServices(cmd *cobra.Command, _ []string) (rerr error) {
	provNames, _ := cmd.Flags().GetStringSlice("providers")
	regions, _ := cmd.Flags().GetStringSlice("regions")
	profile, _ := cmd.Flags().GetString("profile")
	subscription, _ := cmd.Flags().GetString("subscription")
	outputFmt := outputFormat(cmd)
	defer func() { maybeStructuredError(outputFmt, rerr) }()
	filter, _ := cmd.Flags().GetString("filter")
	services, _ := cmd.Flags().GetStringSlice("services")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	checkStrict, _ := cmd.Flags().GetBool("check-strict")

	switch filter {
	case "all", "covered", "uncovered", "synthetic", "upstream-missing":
	default:
		return fmt.Errorf("--filter must be one of all|covered|uncovered|synthetic|upstream-missing; got %q", filter)
	}
	switch outputFmt {
	case "markdown", "md", "table", "json", "jsonl", "csv":
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", outputFmt)
	}

	covProviders, err := resolveCoverageProviders(provNames)
	if err != nil {
		return err
	}
	if len(covProviders) == 0 {
		return fmt.Errorf("no coverage providers registered")
	}

	opts := coverage.FetchOptions{Regions: regions, Profile: profile, Subscription: subscription}

	var matrices []coverage.Matrix
	var fetchFailures []string
	for _, p := range covProviders {
		fmt.Fprintf(os.Stderr, "Fetching %s upstream registry...\n", p.Name())
		fetchCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
		upstream, err := p.Fetch(fetchCtx, opts)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: fetch failed: %v (continuing with empty upstream)\n", p.Name(), err)
			fetchFailures = append(fetchFailures, p.Name())
		}
		m := coverage.Build(p.Name(), p.Emits(), p.Aliases(), p.AlgorithmicKey, upstream)
		m.Rows = filterRows(m.Rows, filter, services)
		matrices = append(matrices, m)
	}

	if checkStrict && len(fetchFailures) > 0 {
		return fmt.Errorf("%w: %s; retry or scope --providers", errCoverageRegistryUnreachable, strings.Join(fetchFailures, ", "))
	}

	w := cmd.OutOrStdout()
	switch outputFmt {
	case "json":
		if err := coverage.RenderJSON(w, matrices); err != nil {
			return err
		}
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, m := range matrices {
			for _, r := range m.Rows {
				if err := enc.Encode(r); err != nil {
					return err
				}
			}
		}
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"provider", "service", "disco_type", "upstream_key", "bucket"})
		for _, m := range matrices {
			for _, r := range m.Rows {
				_ = cw.Write([]string{r.Provider, r.Service, r.DiscoType, r.UpstreamKey, string(r.Bucket)})
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
	case "markdown", "md":
		if err := coverage.RenderMarkdown(w, matrices); err != nil {
			return err
		}
	default:
		if err := coverage.RenderTable(w, matrices); err != nil {
			return err
		}
	}

	if checkStrict {
		for _, m := range matrices {
			for _, r := range m.Rows {
				if r.Bucket == coverage.BucketUpstreamMissing {
					return fmt.Errorf("upstream-missing rows present (--check-strict); refresh alias map or scanner emits decl")
				}
			}
		}
	}
	return nil
}

func runCoverageRegions(cmd *cobra.Command, _ []string) (rerr error) {
	provNames, _ := cmd.Flags().GetStringSlice("providers")
	regionFilter, _ := cmd.Flags().GetStringSlice("regions")
	profile, _ := cmd.Flags().GetString("profile")
	subscription, _ := cmd.Flags().GetString("subscription")
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
		fmt.Fprintf(os.Stderr, "Fetching %s region list...\n", p.Name())
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

	if checkStrict && len(fetchFailures) > 0 {
		return fmt.Errorf("%w: %s; retry or scope --providers", errCoverageRegistryUnreachable, strings.Join(fetchFailures, ", "))
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

	// Pre-validate like the services/regions siblings; both resolvers render
	// paths otherwise fall through to the tabwriter table on an unknown value.
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

// selectedAuditors resolves the providers whose resolver registries should be
// audited. An empty provNames (the `--providers` default) selects every
// compiled provider that implements coverage.ResolverAuditor; a non-empty list
// selects exactly those, erroring if a named provider is unknown or lacks
// resolver auditing. Returns a clear error when no auditor is available (e.g. a
// slim build excluding AWS+Azure) so `coverage resolvers` degrades gracefully.
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
// to resolvers that touch one of the named services.
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

// runResolversMissing prints orphan disco types — those never appearing
// as the Source of any EdgeDecl. Optionally filtered to types whose service
// segment matches one of the named services.
func runResolversMissing(w io.Writer, auditors []auditorPair, services []string, outputFmt string) error {
	allowed := lowerSet(services)
	type row struct {
		Provider  string `json:"provider"`
		DiscoType string `json:"disco_type"`
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

// filterRows applies --filter and --services to a row slice.
func filterRows(rows []coverage.Row, filter string, services []string) []coverage.Row {
	allowedSvc := map[string]bool{}
	for _, s := range services {
		allowedSvc[strings.ToLower(s)] = true
	}
	wantBucket := coverage.Bucket(filter)
	out := rows[:0]
	for _, r := range rows {
		if filter != "all" && r.Bucket != wantBucket {
			continue
		}
		if len(allowedSvc) > 0 && !allowedSvc[strings.ToLower(r.Service)] {
			continue
		}
		out = append(out, r)
	}
	return out
}
