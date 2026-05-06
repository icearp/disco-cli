package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/policy"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var (
	checkRulePaths   []string
	checkSeverity    string
	checkOutputFmt   string
	checkExitNonZero bool
	checkTagFilters  []string
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Evaluate Rego policies against discovered resources",
	Long: `Run OPA Rego policies against the local resource DB and emit findings.

Policies are loaded from files or directories passed via --rules (directories
are walked recursively for *.rego). Each module must populate the
data.disco.deny set with finding objects shaped:

  {"id": "...", "severity": "low|medium|high|critical", "message": "...",
   "tags": {...}, "remediation": "...", "ref_url": "..."}

The engine ships in OSS — bring your own policies (Conftest AWS, regula,
in-house bundles). Curated compliance packs (NIST, CIS, PCI-DSS,
Well-Architected) are a paid add-on.

Every resource in the local DB (including provider-managed rows such as
AWS-managed IAM policies and Azure built-in role definitions) is evaluated.
The resource count is printed to stderr; -o json|jsonl|sarif stdout stays
clean for piping.

Input contract: each policy is evaluated once per resource. The input
document carries snake_case keys:
  id, provider, account_id, account_name, type, native_id, name,
  region, zone, status, tags (object), attributes (parsed object),
  created_at, discovered_at, discovered_by, verified_at, verified_by,
  managed_by_provider.
Timestamps are RFC3339; parse via time.parse_rfc3339_ns(input.verified_at)
for freshness-bound controls.

Examples:
  disco check --rules ./policies
  disco check --rules ./policies --severity high -o jsonl
  disco check --rules ./policies -o sarif > findings.sarif
  disco check --rules ./policies --exit-nonzero`,
	RunE: func(cmd *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(checkOutputFmt, rerr) }()
		if len(checkRulePaths) == 0 {
			return fmt.Errorf("--rules is required (path to .rego file or directory)")
		}

		ctx := cmd.Context()

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		eng, err := policy.NewEngine(ctx, checkRulePaths)
		if err != nil {
			return err
		}

		resources, err := loadAllResourcesPaged(db, store.ResourceFilter{IncludeManaged: true})
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Evaluating %d resource(s)\n", len(resources))

		findings, err := eng.Evaluate(ctx, resources)
		if err != nil {
			return err
		}

		if checkSeverity != "" {
			findings = filterBySeverity(findings, checkSeverity)
		}
		if len(checkTagFilters) > 0 {
			findings = filterByTags(findings, checkTagFilters)
		}

		switch checkOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(findings); err != nil {
				return err
			}
		case "jsonl":
			enc := json.NewEncoder(os.Stdout)
			for _, f := range findings {
				if err := enc.Encode(f); err != nil {
					return err
				}
			}
		case "sarif":
			if err := renderCheckSARIF(findings, os.Stdout); err != nil {
				return err
			}
		case "table", "":
			if err := renderCheckTable(findings); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, json, jsonl, sarif)", checkOutputFmt)
		}

		if checkExitNonZero && len(findings) > 0 {
			return fmt.Errorf("%d finding(s)", len(findings))
		}
		return nil
	},
}

// severityRank orders the four conventional levels for `--severity` cutoff.
// Findings whose severity rank is below the cutoff are dropped.
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

func filterBySeverity(fs []policy.Finding, minSeverity string) []policy.Finding {
	cutoff, ok := severityRank[strings.ToLower(minSeverity)]
	if !ok {
		return fs
	}
	out := fs[:0:0]
	for _, f := range fs {
		if severityRank[strings.ToLower(f.Severity)] >= cutoff {
			out = append(out, f)
		}
	}
	return out
}

// filterByTags keeps only findings matching at least one `key=value` (or
// bare `key`) filter — multiple filters OR'd. Bare key matches any value.
func filterByTags(fs []policy.Finding, filters []string) []policy.Finding {
	out := fs[:0:0]
	for _, f := range fs {
		for _, filter := range filters {
			key, value, hasValue := strings.Cut(filter, "=")
			got, present := f.Tags[key]
			if !present {
				continue
			}
			if !hasValue || got == value {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func renderCheckTable(findings []policy.Finding) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Findings: %d\n\n", len(findings))
	if len(findings) == 0 {
		return w.Flush()
	}
	_, _ = fmt.Fprintln(w, "SEVERITY\tRULE\tRESOURCE\tTYPE\tNAME\tREGION\tMESSAGE")
	for _, f := range findings {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			f.Severity, f.ID, short(f.ResourceID), f.Type,
			dashIfEmpty(f.Name), dashIfEmpty(f.Region), f.Message)
	}
	return w.Flush()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func init() {
	checkCmd.Flags().StringSliceVar(&checkRulePaths, "rules", nil, "Rego policy file or directory (repeatable; directories walked for *.rego)")
	checkCmd.Flags().StringVar(&checkSeverity, "severity", "", "Minimum severity to report: low|medium|high|critical")
	checkCmd.Flags().StringVarP(&checkOutputFmt, "output", "o", "table", "Output format: table, json, jsonl, sarif")
	checkCmd.Flags().BoolVar(&checkExitNonZero, "exit-nonzero", false, "Exit 1 if any finding reported")
	checkCmd.Flags().StringSliceVar(&checkTagFilters, "tag", nil, "Keep only findings whose tags match k=v (repeatable; bare k matches any value)")
	rootCmd.AddCommand(checkCmd)
}
