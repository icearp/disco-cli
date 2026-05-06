package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
	awsprov "codeberg.org/icearp/disco/internal/providers/aws"
	"github.com/spf13/cobra"
)

var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Print disco vs upstream cloud-provider type coverage matrix",
	Long: `Compares disco's registered scanners against the live upstream type
registry of each cloud provider:

  - AWS:    CloudFormation ListTypes (Public, Resource)
  - Azure:  ARM Providers/List?$expand=resourceTypes
  - GCP:    Discovery API (https://www.googleapis.com/discovery/v1/apis)

Coverage truth source is the per-scanner emits []TypeDecl declared on each
serviceEntry — disco knows what each scanner upserts, not a static slice
that may have drifted.

Output formats:
  - markdown (default; suitable for README inclusion)
  - table    (tabwriter-aligned plain text)
  - json     (structured matrix slice for tooling)

Bucket model:
  - covered          disco scanner + upstream registry entry both present.
  - uncovered        upstream registry entry has no disco scanner.
  - synthetic        disco-only type (no upstream registry entry expected).
  - upstream-missing disco emits but upstream registry no longer publishes
                     — drift signal. Surface via --check-strict for CI gating.

Examples:
  disco coverage
  disco coverage --provider gcp
  disco coverage --provider aws --filter uncovered
  disco coverage -o json | jq '.[].rows[] | select(.bucket=="upstream-missing")'
  disco coverage --check-strict`,
	RunE: runCoverage,
}

func init() {
	coverageCmd.Flags().String("provider", "", "Limit to one provider (aws|azure|gcp); default = all registered")
	coverageCmd.Flags().String("region", "us-east-1", "AWS region for the CloudFormation API call (--provider aws only)")
	coverageCmd.Flags().String("profile", "", "AWS profile name (--provider aws only)")
	coverageCmd.Flags().String("subscription", "", "Azure subscription ID (--provider azure only); empty = autodetect")
	coverageCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown, table, json")
	coverageCmd.Flags().String("filter", "all", "Filter rows: all, covered, uncovered, synthetic, upstream-missing")
	coverageCmd.Flags().StringSlice("services", nil, "Limit rows to listed services (matched against the row's service segment)")
	coverageCmd.Flags().Duration("timeout", 60*time.Second, "Per-provider live-fetch timeout")
	coverageCmd.Flags().Bool("check-strict", false, "Exit non-zero if any upstream-missing rows are found")
	coverageCmd.Flags().Bool("resolvers", false, "Resolver coverage mode (--provider aws): list every registered resolver and its declared EdgeDecl count; unannotated resolvers (count=0) surface as sweep targets")
	coverageCmd.Flags().Bool("only-unannotated", false, "With --resolvers, omit resolvers that already declare ≥1 EdgeDecl")
	coverageCmd.Flags().Bool("missing-resolvers", false, "Missing-resolver mode (--provider aws): list every emitted disco type that never appears as EdgeDecl.Source — the candidate gap inventory")
	rootCmd.AddCommand(coverageCmd)
}

func runCoverage(cmd *cobra.Command, _ []string) (rerr error) {
	provName, _ := cmd.Flags().GetString("provider")
	region, _ := cmd.Flags().GetString("region")
	profile, _ := cmd.Flags().GetString("profile")
	subscription, _ := cmd.Flags().GetString("subscription")
	outputFmt, _ := cmd.Flags().GetString("output")
	defer func() { maybeStructuredError(outputFmt, rerr) }()
	filter, _ := cmd.Flags().GetString("filter")
	services, _ := cmd.Flags().GetStringSlice("services")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	checkStrict, _ := cmd.Flags().GetBool("check-strict")
	resolversMode, _ := cmd.Flags().GetBool("resolvers")
	onlyUnannotated, _ := cmd.Flags().GetBool("only-unannotated")
	missingMode, _ := cmd.Flags().GetBool("missing-resolvers")

	if resolversMode {
		return runResolverCoverage(cmd.OutOrStdout(), provName, onlyUnannotated)
	}
	if missingMode {
		return runMissingResolvers(cmd.OutOrStdout(), provName)
	}

	switch filter {
	case "all", "covered", "uncovered", "synthetic", "upstream-missing":
	default:
		return fmt.Errorf("--filter must be one of all|covered|uncovered|synthetic|upstream-missing; got %q", filter)
	}
	switch outputFmt {
	case "markdown", "table", "json":
	default:
		return fmt.Errorf("--output must be one of markdown|table|json; got %q", outputFmt)
	}

	providers := coverage.All()
	if provName != "" {
		p, ok := coverage.Get(provName)
		if !ok {
			return fmt.Errorf("provider %q has no coverage support; registered: %v", provName, coverage.Names())
		}
		providers = []coverage.Provider{p}
	}
	if len(providers) == 0 {
		return fmt.Errorf("no coverage providers registered")
	}

	opts := coverage.FetchOptions{Region: region, Profile: profile, Subscription: subscription}

	var matrices []coverage.Matrix
	var fetchFailures []string
	for _, p := range providers {
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

	w := cmd.OutOrStdout()
	switch outputFmt {
	case "json":
		if err := coverage.RenderJSON(w, matrices); err != nil {
			return err
		}
	case "table":
		if err := coverage.RenderTable(w, matrices); err != nil {
			return err
		}
	default:
		if err := coverage.RenderMarkdown(w, matrices); err != nil {
			return err
		}
	}

	if checkStrict {
		// Fetch-failure short-circuit: cannot assess drift when registry
		// unreachable. Distinct from real drift so CI consumers can branch
		// on the message rather than treating throttling as a fleet-wide
		// drift event (F9).
		if len(fetchFailures) > 0 {
			return fmt.Errorf("cannot assess --check-strict: upstream registry unreachable for %s; retry or scope --provider", strings.Join(fetchFailures, ", "))
		}
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

// runResolverCoverage prints per-resolver EdgeDecl counts. Surfaces resolvers
// with zero declared edges so sweepers can find unannotated registrations.
// AWS-only today; cross-provider extension would lift `ListResolvers` into
// the coverage.Provider interface and switch on provider here.
func runResolverCoverage(w stdoutWriter, provName string, onlyUnannotated bool) error {
	if provName != "" && provName != "aws" {
		return fmt.Errorf("--resolvers currently supports --provider aws (got %q)", provName)
	}
	infos := awsprov.ListResolvers()
	annotated, unannotated := 0, 0
	fmt.Fprintln(w, "RESOLVER\tEDGES")
	for _, r := range infos {
		if r.EdgeCount == 0 {
			unannotated++
			fmt.Fprintf(w, "%s\t0\n", r.Name)
			continue
		}
		annotated++
		if onlyUnannotated {
			continue
		}
		fmt.Fprintf(w, "%s\t%d\n", r.Name, r.EdgeCount)
	}
	fmt.Fprintf(os.Stderr, "\n%d resolvers total — %d annotated, %d unannotated\n", len(infos), annotated, unannotated)
	return nil
}

// runMissingResolvers prints every emitted AWS disco type that never appears
// as the Source of a declared EdgeDecl. These are the candidate resolver
// gaps — types whose scanned rows produce zero outbound edges. Output is
// sorted by service prefix then disco type so reruns diff cleanly.
func runMissingResolvers(w stdoutWriter, provName string) error {
	if provName != "" && provName != "aws" {
		return fmt.Errorf("--missing-resolvers currently supports --provider aws (got %q)", provName)
	}
	prov, ok := coverage.Get("aws")
	if !ok {
		return fmt.Errorf("aws coverage provider not registered")
	}
	emitted := make(map[string]struct{})
	for _, decl := range prov.Emits() {
		if decl.Leaf {
			// Intentional no-edge type — excluded from gap inventory.
			continue
		}
		emitted[decl.DiscoType] = struct{}{}
	}
	sources := make(map[string]struct{})
	for _, e := range awsprov.CollectResolverEdges() {
		sources[e.Source] = struct{}{}
	}
	orphans := make([]string, 0, len(emitted))
	for t := range emitted {
		if _, has := sources[t]; has {
			continue
		}
		orphans = append(orphans, t)
	}
	sort.Strings(orphans)

	fmt.Fprintln(w, "disco_type\tservice")
	for _, t := range orphans {
		svc := ""
		if i := strings.Index(t, ":"); i >= 0 {
			rest := t[i+1:]
			if j := strings.Index(rest, ":"); j >= 0 {
				svc = rest[:j]
			}
		}
		fmt.Fprintf(w, "%s\t%s\n", t, svc)
	}
	fmt.Fprintf(os.Stderr, "\n%d source-orphan types out of %d emitted\n", len(orphans), len(emitted))
	return nil
}

// stdoutWriter is the minimal io.Writer surface runResolverCoverage needs;
// declared as an alias of cobra's `cmd.OutOrStdout()` return type. Keeps
// the signature decoupled from any specific writer concrete type.
type stdoutWriter interface {
	Write(p []byte) (int, error)
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
