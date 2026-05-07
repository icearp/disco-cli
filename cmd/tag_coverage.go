package cmd

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

// errTagCoverageBelow is the sentinel returned when --min-coverage trips on
// at least one reported key. cmd/root.go maps it to a non-zero exit; the
// deferred maybeStructuredError wrapper skips the JSON envelope so the
// stdout report is the gate's payload.
var errTagCoverageBelow = errors.New("tag coverage below threshold")

var (
	tagCovProvider        string
	tagCovType            string
	tagCovExcludeTypes    []string
	tagCovRegion          string
	tagCovScanID          string
	tagCovSince           = singleSetString{flag: "since"}
	tagCovOutputFmt       string
	tagCovIncludeManaged  bool
	tagCovCaseInsensitive bool
	tagCovMinCoverage     float64
	tagCovMinCovSet       bool
	tagCovExitZero        bool
)

// awsAccessKeyTagRE matches an AWS access-key ID (`AKIA[20-char base32]`)
// when it appears as a tag KEY. Tag keys shaped like access-key IDs are
// always credential leaks pasted into the wrong field — surface separately
// so a CIO scorecard doesn't render them as legitimate coverage rows.
var awsAccessKeyTagRE = regexp.MustCompile(`^AKIA[0-9A-Z]{16}$`)

var tagCoverageCmd = &cobra.Command{
	Use:   "tag-coverage [key...]",
	Short: "Per-tag coverage rate across discovered resources",
	Long: `Reports the fraction of resources carrying each tag key.

Without arguments, walks every distinct tag key found in the DB and prints
its coverage rate. With one or more keys, restricts the report to those
keys only (zero-coverage keys still appear so audit dashboards see the
absent-tag signal).

Customer-managed resources only by default; --include-managed expands the
denominator. Filter scope with --provider / --type / --region — useful for
"tag coverage on EC2 instances only" rollups.

Examples:
  disco tag-coverage owner cost-center
  disco tag-coverage --provider aws --type aws:ec2:instance
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
		var regions []string
		if tagCovRegion != "" {
			regions = []string{tagCovRegion}
		}

		scanID, err := resolveScanID(db, tagCovScanID)
		if err != nil {
			return err
		}
		since, err := parseSince(tagCovSince.val)
		if err != nil {
			return err
		}
		rows, err := loadAllResourcesPaged(db, store.ResourceFilter{
			Provider:       tagCovProvider,
			Types:          types,
			ExcludeTypes:   tagCovExcludeTypes,
			Regions:        regions,
			DiscoveredBy:   scanID,
			Since:          since,
			IncludeManaged: tagCovIncludeManaged,
		})
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
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
// spreadsheet math; the table renderer formats it as percent. Suspicious
// is non-empty when the tag key matches a known credential / leak shape.
type tagCoverage struct {
	Tag        string  `json:"tag"`
	Tagged     int     `json:"tagged"`
	Total      int     `json:"total"`
	Coverage   float64 `json:"coverage"`
	Suspicious string  `json:"suspicious,omitempty"`
}

// buildTagReport walks every resource's tags map and tallies coverage per
// key. With explicit keys, zero-count keys still appear so dashboards see
// the absent-tag signal. Sort order: tagged desc, tag asc.
//
// caseInsensitive folds every tag key to lower-case before tallying, and
// matches user-supplied keys against the folded map — fix for F13 where
// `environment` (0%) and `Environment` (5.5%) silently produced two
// different scorecards. Suspicious-shape keys (regex-matched access-key
// IDs) get a `[suspicious]` annotation rather than rendering as a normal
// coverage row.
func buildTagReport(rows []store.Resource, keys []string, caseInsensitive bool) []tagCoverage {
	total := len(rows)
	counts := map[string]int{}
	// origKey preserves the first observed casing so suspicious-shape
	// regex (uppercase AKIA…) still matches even under --case-insensitive.
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
			out = append(out, makeTagRow(display, origKey[k], c, total))
		}
	} else {
		for _, k := range keys {
			lookup := k
			if caseInsensitive {
				lookup = strings.ToLower(k)
			}
			out = append(out, makeTagRow(k, origKey[lookup], counts[lookup], total))
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

func makeTagRow(display, original string, c, total int) tagCoverage {
	row := tagCoverage{Tag: display, Tagged: c, Total: total, Coverage: ratio(c, total)}
	probe := original
	if probe == "" {
		probe = display
	}
	if awsAccessKeyTagRE.MatchString(probe) {
		row.Suspicious = "aws-access-key-id"
	}
	return row
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
	case "table", "":
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "TAG\tTAGGED\tTOTAL\tCOVERAGE\tFLAG")
		for _, r := range rep {
			flag := ""
			if r.Suspicious != "" {
				flag = "[suspicious:" + r.Suspicious + "]"
			}
			_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%.1f%%\t%s\n", r.Tag, r.Tagged, r.Total, r.Coverage*100, flag)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, json, csv)", format)
	}
}

func init() {
	tagCoverageCmd.Flags().StringVarP(&tagCovProvider, "provider", "p", "", "Filter by provider (aws, azure, gcp)")
	tagCoverageCmd.Flags().StringVarP(&tagCovType, "type", "t", "", "Filter by resource type")
	tagCoverageCmd.Flags().StringSliceVar(&tagCovExcludeTypes, "exclude-types", nil, "Comma-separated resource types to exclude from the denominator")
	tagCoverageCmd.Flags().StringVar(&tagCovScanID, "scan-id", "", "Restrict to one scan run; accepts a scan ID or 'latest'")
	tagCoverageCmd.Flags().Var(&tagCovSince, "since", "Restrict to rows first-seen on or after this timestamp (RFC3339 or YYYY-MM-DD)")
	tagCoverageCmd.Flags().StringVarP(&tagCovRegion, "region", "r", "", "Filter by region")
	tagCoverageCmd.Flags().StringVarP(&tagCovOutputFmt, "output", "o", "table", "Output format: table, json, csv")
	tagCoverageCmd.Flags().BoolVar(&tagCovIncludeManaged, "include-managed", false, "Include provider-managed resources in the denominator")
	tagCoverageCmd.Flags().BoolVar(&tagCovCaseInsensitive, "case-insensitive", false, "Fold tag keys to lower-case so 'environment' and 'Environment' aggregate into one row")
	tagCoverageCmd.Flags().Float64Var(&tagCovMinCoverage, "min-coverage", 0,
		"Coverage threshold in [0,1]; if any reported key falls below, exit non-zero (use --exit-zero to override)")
	tagCoverageCmd.Flags().BoolVar(&tagCovExitZero, "exit-zero", false,
		"Force exit 0 even when --min-coverage is breached (still renders the report)")
	rootCmd.AddCommand(tagCoverageCmd)
}
