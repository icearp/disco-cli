package cmd

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/policy"
	"codeberg.org/icearp/disco/internal/snapshot"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var (
	checkRulePaths        []string
	checkPacks            []string
	checkSeverity         string
	checkOutputFmt        string
	checkExitZero         bool
	checkTagFilters       []string
	checkIncludeManaged   bool
	checkRequireResources bool
	checkMinResources     uint64
)

// persistCheckHook is set by the paid build via cmd/check_paid.go init().
// nil in OSS — `disco check` stays ephemeral. Called from the RunE after
// findings are computed and post-filter-applied; receives the post-filter
// slice so `findings list` shape matches the same-invocation stdout.
var persistCheckHook func(db *store.Store, paths, packs []string, severity string, resourceCount int, findings []policy.Finding) error

// checkNeedsWriteHook is set by the paid build to signal that this invocation
// needs a writable DB (e.g. `--persist`). nil in OSS. When nil or returning
// false, `check` opens the DB read-only — see WS1 in
// focus-group/SUMMARY.md for why writable opens flip the SQLite WAL header
// and break snapshot/verify roundtrips.
var checkNeedsWriteHook func() bool

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Evaluate Rego policies against discovered resources",
	Long: `Run OPA Rego policies against the local resource DB and emit findings.

Policies are loaded from files or directories passed via --rules (directories
are walked recursively for *.rego). Each module must populate the
data.disco.deny set with finding objects shaped:

  {"id": "...", "severity": "low|medium|high|critical", "message": "...",
   "tags": {...}, "remediation": "...", "ref_url": "..."}

The engine ships in OSS. Two ways to feed it rules:

  --rules <file|dir>   Bring your own policies (Conftest AWS, regula,
                       in-house bundles). Repeatable.
  --packs <name,...>   Load bundled OSS packs. Available:
                         aws-waf — 5-rule AWS Well-Architected sample pack
                                   (one or two rules per pillar)

Both flags compose; both run in one pass against the full population.

Curated full packs (Well-Architected complete, CIS-AWS-Foundations,
NIST 800-53, PCI-DSS, ISO 27001) are a paid add-on.

Every resource in the local DB (including provider-managed rows such as
AWS-managed IAM policies and Azure built-in role definitions) is evaluated.
The resource count is printed to stderr; -o json|jsonl|sarif stdout stays
clean for piping.

Input contract: each policy is evaluated once per resource. The input
document carries snake_case keys:
  contract_version (currently "1"), id, provider, account_id,
  account_name, type, native_id, name, region, zone, status,
  tags (object), attributes (parsed object), created_at, discovered_at,
  discovered_by, verified_at, verified_by, managed_by_provider.
Pin BYO rules against contract_version (input.contract_version == "1")
so future input-shape changes fail loud rather than silently misfiring.
Timestamps are RFC3339; parse via time.parse_rfc3339_ns(input.verified_at)
for freshness-bound controls.

SARIF taxonomy keys: tags carrying any of waf_pillar, waf_qid, soc2,
iso27001, pci_dss, nist_800_53 lift into runs[0].taxonomies[] as the
matching framework. Bundled aws-waf rules emit waf_pillar+waf_qid only;
BYO rules adding soc2 / iso27001 / pci_dss / nist_800_53 get the
matching taxonomy automatically. Taxon IDs are the unique tag values,
sorted for byte-stable output. The SARIF runs[0].invocations[0].properties
block carries total_resources_evaluated + scan_ids (when present), and
runs[0].tool.driver.properties carries disco_db_sha256 + rules_sha256 —
the chain ties every finding back to the exact DB and rule bundle.

Exit codes: any reported finding gates the exit code at 1 by default,
so 'disco check' plugs into CI without an extra flag. Pass --exit-zero
to render findings without gating (inventory-only runs that should not
fail the pipeline). Empty findings always exit 0; --severity / --tag
filters that drop every finding likewise yield exit 0.

Examples:
  disco check --packs aws-waf
  disco check --packs aws-waf --severity high
  disco check --rules ./policies --packs aws-waf -o sarif > findings.sarif
  disco check --rules ./policies --exit-zero            # render but never gate`,
	RunE: func(cmd *cobra.Command, _ []string) (rerr error) {
		defer func() {
			// Skip the stdout JSON envelope on the findings-gate sentinel
			// — the findings array IS the payload, the exit code IS the
			// gate. Trailing `{"error":"N finding(s)"}` after the array
			// would break strict consumers (json.load, jq -e). F7.
			if errors.Is(rerr, errFindingsReported) {
				return
			}
			maybeStructuredError(checkOutputFmt, rerr)
		}()
		if len(checkRulePaths) == 0 && len(checkPacks) == 0 {
			return fmt.Errorf("--rules or --packs is required (e.g. --packs aws-waf)")
		}
		switch checkSeverity {
		case "", "low", "medium", "high", "critical":
		default:
			return fmt.Errorf("--severity must be one of low|medium|high|critical (got %q)", checkSeverity)
		}

		ctx := cmd.Context()

		// `check` opens the DB read-only by default. A writable open flips the
		// SQLite WAL header, mutating the file and breaking any subsequent
		// `disco verify` against a snapshot of it. The paid `--persist` path
		// signals via checkNeedsWriteHook that it needs to upgrade.
		needsWrite := checkNeedsWriteHook != nil && checkNeedsWriteHook()
		var (
			db  *store.Store
			err error
		)
		if needsWrite {
			if dbReadOnly {
				return fmt.Errorf("--db-readonly: --persist cannot run in read-only mode")
			}
			db, err = openWriteDB()
		} else {
			db, err = openDB()
		}
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		var modules map[string]string
		if len(checkPacks) > 0 {
			modules, err = policy.LoadPacks(checkPacks)
			if err != nil {
				return err
			}
		}

		eng, err := policy.NewEngine(ctx, checkRulePaths, modules)
		if err != nil {
			return err
		}

		resources, err := loadAllResourcesPaged(db, store.ResourceFilter{IncludeManaged: checkIncludeManaged})
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}
		if err := gateResourceCount(len(resources), checkRequireResources, checkMinResources); err != nil {
			return err
		}

		if checkIncludeManaged {
			fmt.Fprintf(os.Stderr, "Evaluating %d resource(s)\n", len(resources))
		} else {
			managed, mErr := db.CountManaged()
			if mErr != nil {
				// Non-fatal: fall back to the bare count rather than blocking
				// the check on a hygiene-stat read.
				fmt.Fprintf(os.Stderr, "Evaluating %d resource(s)\n", len(resources))
			} else {
				fmt.Fprintf(os.Stderr,
					"Evaluating %d customer-managed resource(s) (%d provider-managed excluded — pass --include-managed to evaluate all)\n",
					len(resources), managed)
			}
		}

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

		if persistCheckHook != nil {
			if err := persistCheckHook(db, checkRulePaths, checkPacks, checkSeverity, len(resources), findings); err != nil {
				return err
			}
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
		case "csv":
			if err := renderCheckCSV(findings); err != nil {
				return err
			}
		case "markdown", "md":
			if err := renderCheckMarkdown(findings); err != nil {
				return err
			}
		case "sarif":
			ev := sarifEvidence{TotalResourcesEvaluated: len(resources)}
			if h, herr := snapshot.HashFile(defaultDBPath()); herr == nil {
				ev.DBSHA256 = h
			}
			if h, herr := policy.RulesSHA256(checkRulePaths, modules); herr == nil {
				ev.RulesSHA256 = h
			}
			if scans, sErr := db.ListScans(); sErr == nil {
				ev.ScanIDs = make([]string, 0, len(scans))
				for _, s := range scans {
					ev.ScanIDs = append(ev.ScanIDs, s.ID)
				}
				sort.Strings(ev.ScanIDs)
			}
			if err := renderCheckSARIF(findings, os.Stdout, ev); err != nil {
				return err
			}
		case "table", "":
			if err := renderCheckTable(findings); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl, sarif)", checkOutputFmt)
		}

		if !checkExitZero && len(findings) > 0 {
			// Default behaviour: any finding reported gates the exit code.
			// Print the count to stderr so CI logs still see the gate
			// fired; sentinel error suppresses the duplicate stdout JSON
			// envelope above. Pass --exit-zero to override (inventory mode).
			fmt.Fprintf(os.Stderr, "%d finding(s)\n", len(findings))
			return errFindingsReported
		}
		return nil
	},
}

// errFindingsReported is a sentinel returned when findings are reported
// and --exit-zero is not set (the default gate). Execute() maps it to
// exit 1; the deferred maybeStructuredError check skips its JSON envelope
// so json/jsonl stdout stays a single parseable document.
var errFindingsReported = errors.New("findings reported")

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

// renderCheckCSV writes findings as CSV with positional-stable columns.
// Mirror of renderCheckTable's column set, minus the visual short() of the
// resource ID — CSV consumers typically want the full ID for joining.
func renderCheckCSV(findings []policy.Finding) error {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()
	if err := w.Write([]string{"severity", "rule", "resource_id", "type", "name", "region", "message"}); err != nil {
		return err
	}
	for _, f := range findings {
		if err := w.Write([]string{f.Severity, f.ID, f.ResourceID, f.Type, f.Name, f.Region, f.Message}); err != nil {
			return err
		}
	}
	return nil
}

// renderCheckMarkdown writes findings as a GitHub-flavoured md table.
// Counts the finding total in a `# ` header so consumers see the scale
// before they scroll.
func renderCheckMarkdown(findings []policy.Finding) error {
	_, _ = fmt.Fprintf(os.Stdout, "# Findings: %d\n\n", len(findings))
	if len(findings) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(findings))
	for _, f := range findings {
		rows = append(rows, []string{
			f.Severity, f.ID, short(f.ResourceID), f.Type,
			dashIfEmpty(f.Name), dashIfEmpty(f.Region), f.Message,
		})
	}
	return renderMarkdownTable(os.Stdout,
		[]string{"Severity", "Rule", "Resource", "Type", "Name", "Region", "Message"}, rows)
}

func init() {
	checkCmd.Flags().StringSliceVar(&checkRulePaths, "rules", nil, "Rego policy file or directory (repeatable; directories walked for *.rego)")
	checkCmd.Flags().StringSliceVar(&checkPacks, "packs", nil, "Comma-separated bundled OSS packs (available: aws-waf)")
	checkCmd.Flags().StringVar(&checkSeverity, "severity", "", "Minimum severity to report: low|medium|high|critical")
	checkCmd.Flags().StringVarP(&checkOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl, sarif")
	checkCmd.Flags().BoolVar(&checkExitZero, "exit-zero", false, "Force exit 0 even when findings are reported (inventory mode; CI override)")
	checkCmd.Flags().StringSliceVar(&checkTagFilters, "tag", nil, "Keep only findings whose tags match k=v (repeatable; bare k matches any value)")
	checkCmd.Flags().BoolVar(&checkIncludeManaged, "include-managed", false, "Include provider-managed resources (built-in roles, AWS-owned prefix lists, etc.) in the evaluation set")
	checkCmd.Flags().BoolVar(&checkRequireResources, "require-resources", false, "Exit non-zero when 0 resources are evaluated (fail-closed gate against an empty / unscanned DB)")
	checkCmd.Flags().Uint64Var(&checkMinResources, "min-resources", 0, "Exit non-zero when fewer than N resources are evaluated (overrides --require-resources when both set)")
	rootCmd.AddCommand(checkCmd)
}
