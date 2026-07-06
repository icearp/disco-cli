package cmd

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

// errTagCoverageBelow is the sentinel returned when --min-coverage trips on
// at least one key. cmd/root.go maps it to non-zero exit; the deferred
// maybeStructuredError wrapper skips the JSON envelope so the stdout report
// is the gate's payload.
var errTagCoverageBelow = errors.New("tag coverage below threshold")

var (
	tagCovProviders        []string
	tagCovType             string
	tagCovExcludeTypes     []string
	tagCovRegions          []string
	tagCovScanID           string
	tagCovDiscoveredSince  = singleSetString{flag: "discovered-since"}
	tagCovDiscoveredBefore = singleSetString{flag: "discovered-before"}
	tagCovCreatedSince     = singleSetString{flag: "created-since"}
	tagCovCreatedBefore    = singleSetString{flag: "created-before"}
	tagCovOutputFmt        string
	tagCovIncludeManaged   bool
	tagCovSkipGlobals      bool
	tagCovCaseInsensitive  bool
	tagCovMinCoverage      float64
	tagCovMinCovSet        bool
	tagCovExitZero         bool
	tagCovRequireResources bool
	tagCovMinResources     uint64
)

var tagCoverageCmd = &cobra.Command{
	Use:   "tag-coverage [key...]",
	Short: "Report per-tag coverage across discovered resources",
	Long: `Reports the fraction of resources carrying each tag key.

Without arguments, walks every distinct tag key found in the DB and prints
its coverage rate. With one or more keys, restricts the report to those
keys only (zero-coverage keys still appear so audit dashboards see the
absent-tag signal).

Customer-managed resources only by default; --include-managed expands the
denominator. Filter scope with --providers / --type / --regions — useful for
"tag coverage on EC2 instances only" rollups.

This is tag-governance coverage (a property of resources disco already
discovered). For scan-capability coverage — what disco knows how to scan vs.
what each cloud actually offers — see 'disco coverage'.`,
	Example: `  disco tag-coverage owner cost-center
  disco tag-coverage --providers aws --type aws:ec2:instance
  disco tag-coverage -o json | jq '.[] | select(.coverage < 0.5)'`,
	RunE: func(cmd *cobra.Command, args []string) (rerr error) {
		defer func() {
			if errors.Is(rerr, errTagCoverageBelow) {
				return
			}
			maybeStructuredError(tagCovOutputFmt, rerr)
		}()
		tagCovMinCovSet = cmd.Flags().Changed("min-coverage")
		if tagCovMinCovSet && (tagCovMinCoverage < 0 || tagCovMinCoverage > 1) {
			return fmt.Errorf("--min-coverage must be in [0,1] (got %v)", tagCovMinCoverage)
		}

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		var types []string
		if tagCovType != "" {
			types = []string{tagCovType}
		}

		scanID, err := resolveScanID(db, tagCovScanID)
		if err != nil {
			return err
		}
		discoveredSince, err := parseTimeFlag("--discovered-since", tagCovDiscoveredSince.val)
		if err != nil {
			return err
		}
		discoveredBefore, err := parseTimeFlag("--discovered-before", tagCovDiscoveredBefore.val)
		if err != nil {
			return err
		}
		createdSince, err := parseTimeFlag("--created-since", tagCovCreatedSince.val)
		if err != nil {
			return err
		}
		createdBefore, err := parseTimeFlag("--created-before", tagCovCreatedBefore.val)
		if err != nil {
			return err
		}
		rows, err := loadAllResourcesPaged(db, store.ResourceFilter{
			Providers:        tagCovProviders,
			Types:            types,
			ExcludeTypes:     tagCovExcludeTypes,
			Regions:          tagCovRegions,
			DiscoveredBy:     scanID,
			DiscoveredSince:  discoveredSince,
			DiscoveredBefore: discoveredBefore,
			CreatedSince:     createdSince,
			CreatedBefore:    createdBefore,
			IncludeManaged:   tagCovIncludeManaged,
			SkipGlobals:      tagCovSkipGlobals,
		})
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}
		if err := gateResourceCount(len(rows), tagCovRequireResources, tagCovMinResources); err != nil {
			return err
		}

		report := buildTagReport(rows, args, tagCovCaseInsensitive)
		if err := renderTagReport(report, tagCovOutputFmt); err != nil {
			return err
		}
		if tagCovMinCovSet && !tagCovExitZero {
			for _, r := range report {
				if r.Coverage < tagCovMinCoverage {
					fmt.Fprintf(os.Stderr, "tag-coverage: %d row(s) below --min-coverage=%.4f\n",
						countBelow(report, tagCovMinCoverage), tagCovMinCoverage)
					return errTagCoverageBelow
				}
			}
		}
		return nil
	},
}

func countBelow(rep []tagCoverage, t float64) int {
	n := 0
	for _, r := range rep {
		if r.Coverage < t {
			n++
		}
	}
	return n
}

// tagCoverage is one row of the report. Coverage is a float in [0,1] for
// spreadsheet math; the table renderer formats it as percent.
type tagCoverage struct {
	Tag      string  `json:"tag"`
	Tagged   int     `json:"tagged"`
	Total    int     `json:"total"`
	Coverage float64 `json:"coverage"`
}

// buildTagReport walks every resource's tags map and tallies coverage per
// key. With explicit keys, zero-count keys still appear so dashboards see
// the absent-tag signal. Sort order: tagged desc, tag asc.
//
// caseInsensitive folds every tag key to lower-case before tallying, and
// matches user-supplied keys against the folded map — fix for F13, where
// `environment` (0%) and `Environment` (5.5%) silently produced two
// scorecards.
func buildTagReport(rows []store.Resource, keys []string, caseInsensitive bool) []tagCoverage {
	total := len(rows)
	counts := map[string]int{}
	// origKey preserves the first observed casing for table display under
	// --case-insensitive.
	origKey := map[string]string{}
	for i := range rows {
		tags, _ := rows[i].Tags()
		for k := range tags {
			key := k
			if caseInsensitive {
				key = strings.ToLower(k)
			}
			counts[key]++
			if _, ok := origKey[key]; !ok {
				origKey[key] = k
			}
		}
	}
	var out []tagCoverage
	if len(keys) == 0 {
		for k, c := range counts {
			display := k
			if o, ok := origKey[k]; ok && !caseInsensitive {
				display = o
			}
			out = append(out, tagCoverage{Tag: display, Tagged: c, Total: total, Coverage: ratio(c, total)})
		}
	} else {
		for _, k := range keys {
			lookup := k
			if caseInsensitive {
				lookup = strings.ToLower(k)
			}
			out = append(out, tagCoverage{Tag: k, Tagged: counts[lookup], Total: total, Coverage: ratio(counts[lookup], total)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tagged != out[j].Tagged {
			return out[i].Tagged > out[j].Tagged
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func renderTagReport(rep []tagCoverage, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	case "jsonl":
		enc := json.NewEncoder(os.Stdout)
		for _, r := range rep {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		if err := w.Write([]string{"tag", "tagged", "total", "coverage"}); err != nil {
			return err
		}
		for _, r := range rep {
			if err := w.Write([]string{
				r.Tag,
				strconv.Itoa(r.Tagged),
				strconv.Itoa(r.Total),
				strconv.FormatFloat(r.Coverage, 'f', 4, 64),
			}); err != nil {
				return err
			}
		}
		return nil
	case "markdown", "md":
		rows := make([][]string, 0, len(rep))
		for _, r := range rep {
			rows = append(rows, []string{
				r.Tag,
				strconv.Itoa(r.Tagged),
				strconv.Itoa(r.Total),
				fmt.Sprintf("%.1f%%", r.Coverage*100),
			})
		}
		return renderMarkdownTable(os.Stdout, []string{"Tag", "Tagged", "Total", "Coverage"}, rows)
	case "table", "":
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "TAG\tTAGGED\tTOTAL\tCOVERAGE")
		for _, r := range rep {
			_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%.1f%%\n", r.Tag, r.Tagged, r.Total, r.Coverage*100)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", format)
	}
}

func init() {
	tagCoverageCmd.Flags().StringSliceVarP(&tagCovProviders, "providers", "p", nil, fmt.Sprintf("Filter by provider(s), comma-separated (%s)", providerListHint()))
	_ = tagCoverageCmd.RegisterFlagCompletionFunc("providers", completeProviderNames)
	tagCoverageCmd.Flags().StringVarP(&tagCovType, "type", "t", "", "Filter by resource type")
	tagCoverageCmd.Flags().StringSliceVar(&tagCovExcludeTypes, "exclude-types", nil, "Comma-separated resource types to exclude from the denominator")
	tagCoverageCmd.Flags().StringVar(&tagCovScanID, "scan-id", "", "Restrict to one scan run; accepts a scan ID or 'latest'")
	tagCoverageCmd.Flags().Var(&tagCovDiscoveredSince, "discovered-since", "Restrict to rows first-seen by disco on or after this timestamp (RFC3339 or YYYY-MM-DD)")
	tagCoverageCmd.Flags().Var(&tagCovDiscoveredBefore, "discovered-before", "Restrict to rows first-seen by disco strictly before this timestamp (pairs with --discovered-since for half-open [since, before) intervals)")
	tagCoverageCmd.Flags().Var(&tagCovCreatedSince, "created-since", "Restrict to rows whose intrinsic CreateDate is on or after this timestamp (rows with no CreateDate are excluded)")
	tagCoverageCmd.Flags().Var(&tagCovCreatedBefore, "created-before", "Restrict to rows whose intrinsic CreateDate is strictly before this timestamp (rows with no CreateDate are excluded)")
	tagCoverageCmd.Flags().StringSliceVarP(&tagCovRegions, "regions", "r", nil, "Filter by region(s), comma-separated")
	tagCoverageCmd.Flags().StringVarP(&tagCovOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = tagCoverageCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))
	tagCoverageCmd.Flags().BoolVar(&tagCovIncludeManaged, "include-managed", false, "Include provider-managed resources in the denominator")
	tagCoverageCmd.Flags().BoolVar(&tagCovSkipGlobals, "exclude-global-region", false, "Exclude rows whose region is \"global\". By default --regions folds globals in.")
	tagCoverageCmd.Flags().BoolVar(&tagCovCaseInsensitive, "case-insensitive", false, "Fold tag keys to lower-case so 'environment' and 'Environment' aggregate into one row")
	tagCoverageCmd.Flags().Float64Var(&tagCovMinCoverage, "min-coverage", 0,
		"Coverage threshold in [0,1]; if any reported key falls below, exit non-zero (use --exit-zero to override)")
	tagCoverageCmd.Flags().BoolVar(&tagCovExitZero, "exit-zero", false,
		"Force exit 0 even when --min-coverage is breached (still renders the report)")
	tagCoverageCmd.Flags().BoolVar(&tagCovRequireResources, "require-resources", false, "Exit non-zero when 0 resources match (fail-closed gate against an empty / unscanned DB)")
	tagCoverageCmd.Flags().Uint64Var(&tagCovMinResources, "min-resources", 0, "Exit non-zero when fewer than N resources match (overrides --require-resources when both set)")
	rootCmd.AddCommand(tagCoverageCmd)
}
