package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/policy"
	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

var (
	findingsOutputFmt  string
	findingsCheckRunID string
	findingsRunSince   = singleSetString{flag: "run-since"}
	findingsSeverity   string
	findingsCategory   string
	findingsType       string
	findingsProvider   string
	findingsFindingID  string
)

var findingsCmd = &cobra.Command{
	Use:   "findings",
	Short: "Query persisted check findings",
	Long: `Query findings recorded by 'disco check --persist'. Pairs with
'disco findings runs' (history of check invocations) and the SARIF
renderer reused from 'disco check'.

Subcommands:
  disco findings list   findings, default to latest run
  disco findings runs   recorded check_run history`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Default behaviour: alias to `findings list`.
		return findingsListCmd.RunE(cmd, nil)
	},
}

var findingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List persisted findings (defaults to the latest check run)",
	Long: `List findings persisted by 'disco check --persist'. Without --check-run-id
the latest run is used; filter further by severity, category, provider, type,
or finding ID. Use 'disco findings runs' to list run IDs.`,
	Example: `  disco findings list
  disco findings list --check-run-id latest --severity high
  disco findings list -p aws -t aws:s3:bucket -o json`,
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(findingsOutputFmt, rerr) }()

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		runID := findingsCheckRunID
		if runID == "" {
			runID = "latest"
		}
		resolved, err := resolveCheckRunID(db, runID)
		if err != nil {
			return err
		}
		since, err := parseTimeFlag("--run-since", findingsRunSince.val)
		if err != nil {
			return err
		}

		rows, err := db.ListFindings(store.FindingFilter{
			CheckRunID: resolved,
			FindingID:  findingsFindingID,
			Severity:   findingsSeverity,
			Category:   findingsCategory,
			Provider:   findingsProvider,
			Type:       findingsType,
			Since:      since,
		})
		if err != nil {
			return fmt.Errorf("list findings: %w", err)
		}

		out := make([]policy.Finding, 0, len(rows))
		for _, r := range rows {
			out = append(out, storedFindingToFinding(r))
		}
		return renderFindings(out, findingsOutputFmt)
	},
}

var findingsRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List recorded check runs",
	Long: `List every check run persisted by 'disco check --persist', newest first,
with its packs, severity filter, and finding count. Copy a run ID into
'disco findings list --check-run-id <id>' to inspect that run's findings.`,
	Example: `  disco findings runs
  disco findings runs --run-since 2025-01-01 -o json`,
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(findingsOutputFmt, rerr) }()

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		runs, err := db.ListCheckRuns()
		if err != nil {
			return fmt.Errorf("list check_runs: %w", err)
		}
		// --run-since reuses singleSetString + parseTimeFlag; filter in-Go
		// because ListCheckRuns has no SQL filter shape for it.
		if findingsRunSince.val != "" {
			cutoff, perr := parseTimeFlag("--run-since", findingsRunSince.val)
			if perr != nil {
				return perr
			}
			filtered := runs[:0]
			for _, r := range runs {
				if r.StartedAt >= cutoff {
					filtered = append(filtered, r)
				}
			}
			runs = filtered
		}
		return renderCheckRuns(runs, findingsOutputFmt)
	},
}

func renderFindings(fs []policy.Finding, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(fs)
	case "jsonl":
		enc := json.NewEncoder(os.Stdout)
		for _, f := range fs {
			if err := enc.Encode(f); err != nil {
				return err
			}
		}
		return nil
	case "sarif":
		// Persisted-findings re-emit can't bind to a single live DB hash —
		// findings span scans by design. Pass an empty evidence stamp so the
		// SARIF doc still validates without claiming a single source.
		return renderCheckSARIF(fs, os.Stdout, sarifEvidence{})
	case "csv":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		if err := w.Write([]string{"finding_id", "severity", "resource_id", "type", "name", "region", "category", "message"}); err != nil {
			return err
		}
		for _, f := range fs {
			if err := w.Write([]string{f.ID, f.Severity, f.ResourceID, f.Type, f.Name, f.Region, f.Category, f.Message}); err != nil {
				return err
			}
		}
		return nil
	case "markdown", "md":
		rows := make([][]string, 0, len(fs))
		for _, f := range fs {
			rows = append(rows, []string{f.ID, f.Severity, f.ResourceID, f.Type, f.Name, f.Region, f.Category, f.Message})
		}
		return renderMarkdownTable(os.Stdout,
			[]string{"Finding", "Severity", "Resource", "Type", "Name", "Region", "Category", "Message"}, rows)
	case "table", "":
		if len(fs) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "No findings.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "FINDING\tSEVERITY\tTYPE\tNAME\tMESSAGE")
		for _, f := range fs {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", f.ID, f.Severity, f.Type, f.Name, f.Message)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl, sarif)", format)
	}
}

func renderCheckRuns(runs []store.CheckRun, format string) error {
	// Re-establish the non-nil contract so `-o json` emits `[]` not `null`
	// on a zero-row query (mirrors resources.go; F6 wire-contract parity).
	if runs == nil {
		runs = []store.CheckRun{}
	}
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runs)
	case "jsonl":
		enc := json.NewEncoder(os.Stdout)
		for _, r := range runs {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		if err := w.Write([]string{"id", "started_at", "finished_at", "packs", "rules_paths", "severity_filter", "resource_count", "finding_count"}); err != nil {
			return err
		}
		for _, r := range runs {
			if err := w.Write([]string{
				r.ID, r.StartedAt, ptrOrEmpty(r.FinishedAt),
				strings.Join(r.Packs, ","), strings.Join(r.RulesPaths, ","),
				ptrOrEmpty(r.SeverityFilter), intPtrOrEmpty(r.ResourceCount), intPtrOrEmpty(r.FindingCount),
			}); err != nil {
				return err
			}
		}
		return nil
	case "markdown", "md":
		rows := make([][]string, 0, len(runs))
		for _, r := range runs {
			rows = append(rows, []string{
				r.ID, r.StartedAt, ptrOrEmpty(r.FinishedAt),
				strings.Join(r.Packs, ","), strings.Join(r.RulesPaths, ","),
				ptrOrEmpty(r.SeverityFilter), intPtrOrEmpty(r.ResourceCount), intPtrOrEmpty(r.FindingCount),
			})
		}
		return renderMarkdownTable(os.Stdout,
			[]string{"ID", "Started", "Finished", "Packs", "Rules", "Severity", "Resources", "Findings"}, rows)
	case "table", "":
		if len(runs) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "No check runs recorded.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tSTARTED\tFINISHED\tPACKS\tRULES\tRESOURCES\tFINDINGS")
		for _, r := range runs {
			rules := strings.Join(r.RulesPaths, ",")
			if rules == "" {
				rules = "-"
			}
			packs := strings.Join(r.Packs, ",")
			if packs == "" {
				packs = "-"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				short(r.ID), r.StartedAt, ptrOrDash(r.FinishedAt),
				packs, rules,
				intPtrOrDashI(r.ResourceCount), intPtrOrDashI(r.FindingCount))
		}
		return w.Flush()
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", format)
	}
}

// storedFindingToFinding maps a store row back to the wire shape so the
// existing OSS renderers (`renderCheckSARIF`, JSON encoders) consume it
// unchanged. Pointer-to-empty-string fields collapse to empty strings.
func storedFindingToFinding(s store.StoredFinding) policy.Finding {
	out := policy.Finding{
		ID:         s.FindingID,
		Severity:   s.Severity,
		Message:    s.Message,
		ResourceID: s.ResourceID,
	}
	if s.Provider != nil {
		out.Provider = *s.Provider
	}
	if s.Type != nil {
		out.Type = *s.Type
	}
	if s.Name != nil {
		out.Name = *s.Name
	}
	if s.Region != nil {
		out.Region = *s.Region
	}
	if s.Category != nil {
		out.Category = *s.Category
	}
	if s.Remediation != nil {
		out.Remediation = *s.Remediation
	}
	if s.RefURL != nil {
		out.RefURL = *s.RefURL
	}
	if s.TagsJSON != nil && *s.TagsJSON != "" {
		_ = json.Unmarshal([]byte(*s.TagsJSON), &out.Tags)
	}
	return out
}

// findingToStored mirrors storedFindingToFinding for the write path
// (the `disco check --persist` flow in cmd/check.go). Empty-string fields
// become NULL.
func findingToStored(f policy.Finding) store.StoredFinding {
	row := store.StoredFinding{
		FindingID:  f.ID,
		ResourceID: f.ResourceID,
		Severity:   f.Severity,
		Message:    f.Message,
	}
	if f.Provider != "" {
		v := f.Provider
		row.Provider = &v
	}
	if f.Type != "" {
		v := f.Type
		row.Type = &v
	}
	if f.Name != "" {
		v := f.Name
		row.Name = &v
	}
	if f.Region != "" {
		v := f.Region
		row.Region = &v
	}
	if f.Category != "" {
		v := f.Category
		row.Category = &v
	}
	if f.Remediation != "" {
		v := f.Remediation
		row.Remediation = &v
	}
	if f.RefURL != "" {
		v := f.RefURL
		row.RefURL = &v
	}
	if len(f.Tags) > 0 {
		b, _ := json.Marshal(f.Tags)
		s := string(b)
		row.TagsJSON = &s
	}
	return row
}

// intPtrOrDashI is the *int variant of the *int rendering helper used by
// disco scans. Local copy avoids an OSS-side rename of intPtrOrDash.
func intPtrOrDashI(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

func init() {
	findingsCmd.PersistentFlags().StringVarP(&findingsOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl, sarif (sarif on list only)")
	// --output is a shared persistent flag, so its completion applies to both
	// `findings list` and `findings runs`; offer only the common set. `sarif`
	// is list-only and would error on `runs`, so it's omitted from the
	// suggestions (it still works when typed on `findings list`).
	_ = findingsCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))

	findingsListCmd.Flags().StringVar(&findingsCheckRunID, "check-run-id", "", "Restrict to one check run; accepts an ID or 'latest' (default)")
	findingsListCmd.Flags().Var(&findingsRunSince, "run-since", "Restrict to runs started on or after this timestamp (RFC3339 or YYYY-MM-DD)")
	findingsListCmd.Flags().StringVar(&findingsSeverity, "severity", "", "Filter by exact severity (low, medium, high, critical)")
	findingsListCmd.Flags().StringVar(&findingsCategory, "category", "", "Filter by category (e.g. aws-waf)")
	findingsListCmd.Flags().StringVarP(&findingsType, "type", "t", "", "Filter by resource type")
	findingsListCmd.Flags().StringVarP(&findingsProvider, "provider", "p", "", fmt.Sprintf("Filter by provider (%s)", providerListHint()))
	findingsListCmd.Flags().StringVar(&findingsFindingID, "finding-id", "", "Filter by Rego rule id (e.g. waf-sec-ebs-encryption-at-rest)")

	findingsRunsCmd.Flags().Var(&findingsRunSince, "run-since", "Restrict to runs started on or after this timestamp (RFC3339 or YYYY-MM-DD)")

	findingsCmd.AddCommand(findingsListCmd)
	findingsCmd.AddCommand(findingsRunsCmd)
	rootCmd.AddCommand(findingsCmd)
}
