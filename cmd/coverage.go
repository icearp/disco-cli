package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
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
	rootCmd.AddCommand(coverageCmd)
}

func runCoverage(cmd *cobra.Command, _ []string) error {
	provName, _ := cmd.Flags().GetString("provider")
	region, _ := cmd.Flags().GetString("region")
	profile, _ := cmd.Flags().GetString("profile")
	subscription, _ := cmd.Flags().GetString("subscription")
	outputFmt, _ := cmd.Flags().GetString("output")
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
	for _, p := range providers {
		fmt.Fprintf(os.Stderr, "Fetching %s upstream registry...\n", p.Name())
		fetchCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
		upstream, err := p.Fetch(fetchCtx, opts)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: fetch failed: %v (continuing with empty upstream)\n", p.Name(), err)
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
