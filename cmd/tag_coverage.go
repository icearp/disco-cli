package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var (
	tagCovProvider       string
	tagCovType           string
	tagCovExcludeTypes   []string
	tagCovRegion         string
	tagCovScanID         string
	tagCovOutputFmt      string
	tagCovIncludeManaged bool
)

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
	RunE: func(_ *cobra.Command, args []string) (rerr error) {
		defer func() { maybeStructuredError(tagCovOutputFmt, rerr) }()

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
		rows, err := loadAllResourcesPaged(db, store.ResourceFilter{
			Provider:       tagCovProvider,
			Types:          types,
			ExcludeTypes:   tagCovExcludeTypes,
			Regions:        regions,
			DiscoveredBy:   scanID,
			IncludeManaged: tagCovIncludeManaged,
		})
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}

		report := buildTagReport(rows, args)
		return renderTagReport(report, tagCovOutputFmt)
	},
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
func buildTagReport(rows []store.Resource, keys []string) []tagCoverage {
	total := len(rows)
	counts := map[string]int{}
	for i := range rows {
		tags, _ := rows[i].Tags()
		for k := range tags {
			counts[k]++
		}
	}
	var out []tagCoverage
	if len(keys) == 0 {
		for k, c := range counts {
			out = append(out, tagCoverage{Tag: k, Tagged: c, Total: total, Coverage: ratio(c, total)})
		}
	} else {
		for _, k := range keys {
			out = append(out, tagCoverage{Tag: k, Tagged: counts[k], Total: total, Coverage: ratio(counts[k], total)})
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
		_, _ = fmt.Fprintln(w, "TAG\tTAGGED\tTOTAL\tCOVERAGE")
		for _, r := range rep {
			_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%.1f%%\n", r.Tag, r.Tagged, r.Total, r.Coverage*100)
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
	tagCoverageCmd.Flags().StringVarP(&tagCovRegion, "region", "r", "", "Filter by region")
	tagCoverageCmd.Flags().StringVarP(&tagCovOutputFmt, "output", "o", "table", "Output format: table, json, csv")
	tagCoverageCmd.Flags().BoolVar(&tagCovIncludeManaged, "include-managed", false, "Include provider-managed resources in the denominator")
	rootCmd.AddCommand(tagCoverageCmd)
}
