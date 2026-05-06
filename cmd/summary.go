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
	summaryProvider       string
	summaryRegion         string
	summaryExcludeTypes   []string
	summaryOutputFmt      string
	summaryTopTypes       int
	summaryIncludeManaged bool
)

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Portfolio rollup of discovered resources",
	Long: `Single-page rollup answering "what do we own?" — counts by provider,
region, and top-N resource types, plus an as-of timestamp from the most
recent scan.

Headline counts are dominated by noisy types (CloudWatch log streams in
particular). Use --exclude-types to mute specific types from the
denominator across all three sections; the same flag works on
'disco list' and 'disco tag-coverage'.

Output formats: table (default), json, csv. JSON envelope shape:

  {
    "as_of":       "<RFC3339 timestamp from latest scan or empty>",
    "total":       <int>,
    "by_provider": [{"provider": "aws", "count": 934}],
    "by_region":   [{"region": "us-east-2", "count": 894}],
    "by_type":     [{"type": "aws:logs:log-stream", "count": 855}]
  }

CSV is long-form: dimension,value,count (one row per bucket).

Examples:
  disco summary
  disco summary --exclude-types aws:logs:log-stream
  disco summary --provider aws -o json | jq '.by_type'
  disco summary --include-managed --top-types 25`,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(summaryOutputFmt, rerr) }()

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		var regions []string
		if summaryRegion != "" {
			regions = []string{summaryRegion}
		}
		rows, err := loadAllResourcesPaged(db, store.ResourceFilter{
			Provider:       summaryProvider,
			ExcludeTypes:   summaryExcludeTypes,
			Regions:        regions,
			IncludeManaged: summaryIncludeManaged,
		})
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}

		asOf := ""
		if scans, sErr := db.ListScans(); sErr == nil && len(scans) > 0 {
			asOf = scans[0].StartedAt
		}

		report := buildSummary(rows, asOf, summaryTopTypes)
		return renderSummary(report, summaryOutputFmt)
	},
}

// summaryBucket is a single (dimension-value, count) pair. Same shape for
// each of the three sections — JSON tags differ via the dedicated wrapper
// types so consumers can decode by section.
type providerBucket struct {
	Provider string `json:"provider"`
	Count    int    `json:"count"`
}

type regionBucket struct {
	Region string `json:"region"`
	Count  int    `json:"count"`
}

type typeBucket struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// summaryReport is the JSON envelope. TypeBucketsTotal preserves the
// pre-truncation distinct-type count so the table renderer can show
// "top N of M" without recomputing.
type summaryReport struct {
	AsOf             string           `json:"as_of"`
	Total            int              `json:"total"`
	ByProvider       []providerBucket `json:"by_provider"`
	ByRegion         []regionBucket   `json:"by_region"`
	ByType           []typeBucket     `json:"by_type"`
	TypeBucketsTotal int              `json:"type_buckets_total"`
}

func buildSummary(rows []store.Resource, asOf string, topTypes int) summaryReport {
	provCounts := map[string]int{}
	regionCounts := map[string]int{}
	typeCounts := map[string]int{}
	for i := range rows {
		r := &rows[i]
		provCounts[r.Provider]++
		region := "(global)"
		if r.Region != nil && *r.Region != "" {
			region = *r.Region
		}
		regionCounts[region]++
		typeCounts[r.Type]++
	}

	provs := make([]providerBucket, 0, len(provCounts))
	for k, c := range provCounts {
		provs = append(provs, providerBucket{Provider: k, Count: c})
	}
	sort.Slice(provs, func(i, j int) bool {
		if provs[i].Count != provs[j].Count {
			return provs[i].Count > provs[j].Count
		}
		return provs[i].Provider < provs[j].Provider
	})

	regs := make([]regionBucket, 0, len(regionCounts))
	for k, c := range regionCounts {
		regs = append(regs, regionBucket{Region: k, Count: c})
	}
	sort.Slice(regs, func(i, j int) bool {
		if regs[i].Count != regs[j].Count {
			return regs[i].Count > regs[j].Count
		}
		return regs[i].Region < regs[j].Region
	})

	types := make([]typeBucket, 0, len(typeCounts))
	for k, c := range typeCounts {
		types = append(types, typeBucket{Type: k, Count: c})
	}
	sort.Slice(types, func(i, j int) bool {
		if types[i].Count != types[j].Count {
			return types[i].Count > types[j].Count
		}
		return types[i].Type < types[j].Type
	})
	totalTypes := len(types)
	if topTypes > 0 && len(types) > topTypes {
		types = types[:topTypes]
	}

	return summaryReport{
		AsOf:             asOf,
		Total:            len(rows),
		ByProvider:       provs,
		ByRegion:         regs,
		ByType:           types,
		TypeBucketsTotal: totalTypes,
	}
}

func renderSummary(rep summaryReport, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	case "csv":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		if err := w.Write([]string{"dimension", "value", "count"}); err != nil {
			return err
		}
		for _, b := range rep.ByProvider {
			if err := w.Write([]string{"provider", b.Provider, strconv.Itoa(b.Count)}); err != nil {
				return err
			}
		}
		for _, b := range rep.ByRegion {
			if err := w.Write([]string{"region", b.Region, strconv.Itoa(b.Count)}); err != nil {
				return err
			}
		}
		for _, b := range rep.ByType {
			if err := w.Write([]string{"type", b.Type, strconv.Itoa(b.Count)}); err != nil {
				return err
			}
		}
		return nil
	case "table", "":
		header := fmt.Sprintf("Disco summary — %d resources", rep.Total)
		if rep.AsOf != "" {
			header += ", as of " + rep.AsOf
		}
		_, _ = fmt.Fprintln(os.Stdout, header)
		_, _ = fmt.Fprintln(os.Stdout)

		printSection := func(title string, rows [][2]string) {
			_, _ = fmt.Fprintln(os.Stdout, title)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, r := range rows {
				_, _ = fmt.Fprintf(w, "  %s\t%s\n", r[0], r[1])
			}
			_ = w.Flush()
			_, _ = fmt.Fprintln(os.Stdout)
		}

		provRows := make([][2]string, 0, len(rep.ByProvider))
		for _, b := range rep.ByProvider {
			provRows = append(provRows, [2]string{b.Provider, strconv.Itoa(b.Count)})
		}
		printSection("BY PROVIDER", provRows)

		regRows := make([][2]string, 0, len(rep.ByRegion))
		for _, b := range rep.ByRegion {
			regRows = append(regRows, [2]string{b.Region, strconv.Itoa(b.Count)})
		}
		printSection("BY REGION", regRows)

		typeTitle := "BY TYPE"
		if rep.TypeBucketsTotal > len(rep.ByType) {
			typeTitle = fmt.Sprintf("BY TYPE (top %d of %d)", len(rep.ByType), rep.TypeBucketsTotal)
		}
		typeRows := make([][2]string, 0, len(rep.ByType))
		for _, b := range rep.ByType {
			typeRows = append(typeRows, [2]string{b.Type, strconv.Itoa(b.Count)})
		}
		printSection(typeTitle, typeRows)
		return nil
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, json, csv)", format)
	}
}

func init() {
	summaryCmd.Flags().StringVarP(&summaryProvider, "provider", "p", "", "Filter by provider (aws, azure, gcp)")
	summaryCmd.Flags().StringVarP(&summaryRegion, "region", "r", "", "Filter by region")
	summaryCmd.Flags().StringSliceVar(&summaryExcludeTypes, "exclude-types", nil, "Comma-separated resource types to exclude (e.g. aws:logs:log-stream)")
	summaryCmd.Flags().StringVarP(&summaryOutputFmt, "output", "o", "table", "Output format: table, json, csv")
	summaryCmd.Flags().IntVar(&summaryTopTypes, "top-types", 10, "Number of top resource types to show (0 = all)")
	summaryCmd.Flags().BoolVar(&summaryIncludeManaged, "include-managed", false, "Include provider-managed resources in the denominator")
	rootCmd.AddCommand(summaryCmd)
}
