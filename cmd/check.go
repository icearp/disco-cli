package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/rules"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var (
	checkRulePaths   []string
	checkIncBuiltins bool
	checkSeverity    string
	checkRuleIDs     []string
	checkOutputFmt   string
	checkExitNonZero bool
	checkTagFilters  []string
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Evaluate rules against discovered resources",
	Long: `Run rule checks against the local resource DB and emit findings.

Rules are loaded from files or directories passed via --rules (directories are
walked recursively for *.yaml and *.yml). Embedded builtins are included by
default; disable with --builtins=false.

Examples:
  disco check
  disco check --severity high -o jsonl
  disco check --rules ./rules --builtins=false --exit-nonzero
  disco check --rule aws-sg-open-ssh -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := store.Open(defaultDBPath())
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		all, err := rules.Load(checkRulePaths...)
		if err != nil {
			return err
		}
		if checkIncBuiltins {
			bi, err := rules.Builtins()
			if err != nil {
				return err
			}
			all = append(all, bi...)
		}

		if len(checkRuleIDs) > 0 {
			all = filterByIDs(all, checkRuleIDs)
		}

		minSev := rules.Severity("")
		if checkSeverity != "" {
			s, err := rules.ParseSeverity(checkSeverity)
			if err != nil {
				return err
			}
			minSev = s
		}

		findings, err := rules.Evaluate(db, all, minSev)
		if err != nil {
			return err
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
		case "table", "":
			if err := renderCheckTable(findings); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, json, jsonl)", checkOutputFmt)
		}

		if checkExitNonZero && len(findings) > 0 {
			return fmt.Errorf("%d finding(s)", len(findings))
		}
		return nil
	},
}

// filterByTags keeps only findings matching at least one of the supplied
// `key=value` (or bare `key`) tag filters. Multiple filters are OR'd — a
// finding tagged `cis-aws=5.3` passes either `--tag cis-aws=5.3` or
// `--tag cis-aws`.
func filterByTags(fs []rules.Finding, filters []string) []rules.Finding {
	out := fs[:0:0]
	for _, f := range fs {
		for _, filter := range filters {
			key, value, _ := strings.Cut(filter, "=")
			if f.HasTag(key, value) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// filterByIDs returns only rules whose ID is in ids.
func filterByIDs(rs []rules.Rule, ids []string) []rules.Rule {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := rs[:0:0]
	for _, r := range rs {
		if want[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

func renderCheckTable(findings []rules.Finding) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Findings: %d\n\n", len(findings))
	if len(findings) == 0 {
		return w.Flush()
	}
	_, _ = fmt.Fprintln(w, "SEVERITY\tRULE\tRESOURCE\tTYPE\tNAME\tREGION\tMESSAGE")
	for _, f := range findings {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			f.Severity, f.RuleID, short(f.ResourceID), f.Type,
			ptrOrDash(f.Name), ptrOrDash(f.Region), f.Message)
	}
	return w.Flush()
}

func init() {
	checkCmd.Flags().StringSliceVar(&checkRulePaths, "rules", nil, "Rules file or directory (repeatable; directories walked for *.yaml/*.yml)")
	checkCmd.Flags().BoolVar(&checkIncBuiltins, "builtins", true, "Include embedded builtin rules")
	checkCmd.Flags().StringVar(&checkSeverity, "severity", "", "Minimum severity to report: low|medium|high|critical")
	checkCmd.Flags().StringSliceVar(&checkRuleIDs, "rule", nil, "Run only named rule(s) (repeatable)")
	checkCmd.Flags().StringVarP(&checkOutputFmt, "output", "o", "table", "Output format: table, json, jsonl")
	checkCmd.Flags().BoolVar(&checkExitNonZero, "exit-nonzero", false, "Exit 1 if any finding reported")
	checkCmd.Flags().StringSliceVar(&checkTagFilters, "tag", nil, "Keep only findings whose rule has tag k=v (repeatable; bare k matches any value)")
	rootCmd.AddCommand(checkCmd)
}
