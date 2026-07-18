package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

var (
	summaryProviders        []string
	summaryRegions          []string
	summaryExcludeTypes     []string
	summaryScanID           string
	summaryDiscoveredSince  = singleSetString{flag: "discovered-since"}
	summaryDiscoveredBefore = singleSetString{flag: "discovered-before"}
	summaryCreatedSince     = singleSetString{flag: "created-since"}
	summaryCreatedBefore    = singleSetString{flag: "created-before"}
	summaryOutputFmt        string
	summaryTopTypes         int
	summaryIncludeManaged   bool
	summarySkipGlobals      bool
	summaryRequireResources bool
	summaryMinResources     uint64
)

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Summarize discovered resources (portfolio rollup)",
	Args:  cobra.NoArgs,
	Long: `Single-page rollup answering "what do we own?" — counts by provider,
region, and top-N resource types, plus an as-of timestamp from the most
recent scan.

Headline counts are dominated by noisy types (CloudWatch log streams in
particular). Use --exclude-types to mute specific types from the
denominator across all three sections; the same flag works on
'disco resources' and 'disco tag-coverage'.

Output formats: table (default), markdown, csv, json, jsonl. JSON envelope shape:

  {
    "as_of":              "<RFC3339 timestamp from latest scan or empty>",
    "total":              <int>,
    "managed_included":   <bool — echoes --include-managed>,
    "by_provider":        [{"provider": "aws", "count": 934}],
    "by_account":         [{"account_id": "123…", "account_name": "prod", "count": 600}],
    "by_region":          [{"region": "us-east-2", "count": 894}],
    "by_type":            [{"type": "aws:logs:log-stream", "count": 855}],
    "type_buckets_total": <int — distinct types pre --top-types truncation>
  }

CSV and jsonl are long-form: dimension,value,count (one row/line per bucket).`,
	Example: `  disco summary
  disco summary --exclude-types aws:logs:log-stream
  disco summary --providers aws -o json | jq '.by_type'
  disco summary --include-managed --top-types 25`,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(summaryOutputFmt, rerr) }()

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		scanID, err := resolveScanID(db, summaryScanID)
		if err != nil {
			return err
		}
		discoveredSince, err := parseTimeFlag("--discovered-since", summaryDiscoveredSince.val)
		if err != nil {
			return err
		}
		discoveredBefore, err := parseTimeFlag("--discovered-before", summaryDiscoveredBefore.val)
		if err != nil {
			return err
		}
		createdSince, err := parseTimeFlag("--created-since", summaryCreatedSince.val)
		if err != nil {
			return err
		}
		createdBefore, err := parseTimeFlag("--created-before", summaryCreatedBefore.val)
		if err != nil {
			return err
		}
		rows, err := loadAllResourcesPaged(db, store.ResourceFilter{
			Providers:        summaryProviders,
			ExcludeTypes:     summaryExcludeTypes,
			Regions:          summaryRegions,
			DiscoveredBy:     scanID,
			DiscoveredSince:  discoveredSince,
			DiscoveredBefore: discoveredBefore,
			CreatedSince:     createdSince,
			CreatedBefore:    createdBefore,
			IncludeManaged:   summaryIncludeManaged,
			SkipGlobals:      summarySkipGlobals,
		})
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}

		asOf := ""
		if scans, sErr := db.ListScans(); sErr == nil && len(scans) > 0 {
			// scans[0].StartedAt is SQLite-flavoured (`YYYY-MM-DD HH:MM:SS`);
			// renderJSON / consumers expect RFC3339 to match resource-row
			// timestamps and the disco check input contract.
			asOf = store.ToRFC3339(scans[0].StartedAt)
		}

		if err := gateResourceCount(len(rows), summaryRequireResources, summaryMinResources); err != nil {
			return err
		}
		report := buildSummary(rows, asOf, summaryTopTypes, summaryIncludeManaged)
		return renderSummary(report, summaryOutputFmt)
	},
}

// summaryBucket is a single (dimension-value, count) pair, same shape
// across all three sections; JSON tags differ via dedicated wrapper
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

type accountBucket struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName,omitempty"`
	Count       int    `json:"count"`
}

// summaryReport is the JSON envelope. TypeBucketsTotal preserves the
// pre-truncation distinct-type count so the table renderer can show "top
// N of M" without recomputing. ManagedIncluded echoes --include-managed
// so consumers can disambiguate customer-only vs customer+managed totals
// without inspecting the invocation (F5 fix).
type summaryReport struct {
	AsOf             string           `json:"asOf"`
	Total            int              `json:"total"`
	ManagedIncluded  bool             `json:"managedIncluded"`
	ByProvider       []providerBucket `json:"byProvider"`
	ByAccount        []accountBucket  `json:"byAccount"`
	ByRegion         []regionBucket   `json:"byRegion"`
	ByType           []typeBucket     `json:"byType"`
	TypeBucketsTotal int              `json:"typeBucketsTotal"`
}

func buildSummary(rows []store.Resource, asOf string, topTypes int, managedIncluded bool) summaryReport {
	provCounts := map[string]int{}
	regionCounts := map[string]int{}
	typeCounts := map[string]int{}
	acctCounts := map[string]int{}
	acctNames := map[string]string{}
	for i := range rows {
		r := &rows[i]
		provCounts[r.Provider]++
		region := "(global)"
		if r.Region != nil && *r.Region != "" {
			region = *r.Region
		}
		regionCounts[region]++
		typeCounts[r.Type]++
		acctCounts[r.AccountID]++
		if r.AccountName != nil && *r.AccountName != "" {
			acctNames[r.AccountID] = *r.AccountName
		}
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

	accts := make([]accountBucket, 0, len(acctCounts))
	for id, c := range acctCounts {
		accts = append(accts, accountBucket{AccountID: id, AccountName: acctNames[id], Count: c})
	}
	sort.Slice(accts, func(i, j int) bool {
		if accts[i].Count != accts[j].Count {
			return accts[i].Count > accts[j].Count
		}
		return accts[i].AccountID < accts[j].AccountID
	})

	return summaryReport{
		AsOf:             asOf,
		Total:            len(rows),
		ManagedIncluded:  managedIncluded,
		ByProvider:       provs,
		ByAccount:        accts,
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
	case "jsonl":
		return renderSummaryJSONL(rep)
	case "csv":
		return renderSummaryCSV(rep)
	case "markdown", "md":
		return renderSummaryMarkdown(rep)
	case "table", "":
		return renderSummaryTable(rep)
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", format)
	}
}

// renderSummaryJSONL emits one JSON object per bucket, mirroring the long-form
// CSV shape (dimension,value,count) so `disco summary -o jsonl | jq` streams
// rows like the other reports do.
func renderSummaryJSONL(rep summaryReport) error {
	enc := json.NewEncoder(os.Stdout)
	type line struct {
		Dimension string `json:"dimension"`
		Value     string `json:"value"`
		Count     int    `json:"count"`
	}
	emit := func(dim, val string, count int) error {
		return enc.Encode(line{Dimension: dim, Value: val, Count: count})
	}
	for _, b := range rep.ByProvider {
		if err := emit("provider", b.Provider, b.Count); err != nil {
			return err
		}
	}
	for _, b := range rep.ByAccount {
		label := b.AccountID
		if b.AccountName != "" {
			label = b.AccountID + " (" + b.AccountName + ")"
		}
		if err := emit("account", label, b.Count); err != nil {
			return err
		}
	}
	for _, b := range rep.ByRegion {
		if err := emit("region", b.Region, b.Count); err != nil {
			return err
		}
	}
	for _, b := range rep.ByType {
		if err := emit("type", b.Type, b.Count); err != nil {
			return err
		}
	}
	return nil
}

func renderSummaryCSV(rep summaryReport) error {
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
	for _, b := range rep.ByAccount {
		label := b.AccountID
		if b.AccountName != "" {
			label = b.AccountID + " (" + b.AccountName + ")"
		}
		if err := w.Write([]string{"account", label, strconv.Itoa(b.Count)}); err != nil {
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
}

func renderSummaryMarkdown(rep summaryReport) error {
	scope := "customer-managed only"
	if rep.ManagedIncluded {
		scope = "incl. provider-managed"
	}
	header := fmt.Sprintf("# Disco summary — %d resources (%s)", rep.Total, scope)
	if rep.AsOf != "" {
		header += ", as of " + rep.AsOf
	}
	_, _ = fmt.Fprintln(os.Stdout, header)
	_, _ = fmt.Fprintln(os.Stdout)

	printMDSection := func(title string, rows [][]string, headers []string) {
		if len(rows) == 0 {
			return
		}
		_, _ = fmt.Fprintf(os.Stdout, "## %s\n\n", title)
		_ = renderMarkdownTable(os.Stdout, headers, rows)
		_, _ = fmt.Fprintln(os.Stdout)
	}

	provRows := make([][]string, 0, len(rep.ByProvider))
	for _, b := range rep.ByProvider {
		provRows = append(provRows, []string{b.Provider, strconv.Itoa(b.Count)})
	}
	printMDSection("BY PROVIDER", provRows, []string{"Provider", "Count"})

	acctRows := make([][]string, 0, len(rep.ByAccount))
	for _, b := range rep.ByAccount {
		label := b.AccountID
		if b.AccountName != "" {
			label = b.AccountID + " (" + b.AccountName + ")"
		}
		acctRows = append(acctRows, []string{label, strconv.Itoa(b.Count)})
	}
	printMDSection("BY ACCOUNT", acctRows, []string{"Account", "Count"})

	regRows := make([][]string, 0, len(rep.ByRegion))
	for _, b := range rep.ByRegion {
		regRows = append(regRows, []string{b.Region, strconv.Itoa(b.Count)})
	}
	printMDSection("BY REGION", regRows, []string{"Region", "Count"})

	typeTitle := "BY TYPE"
	if rep.TypeBucketsTotal > len(rep.ByType) {
		typeTitle = fmt.Sprintf("BY TYPE (top %d of %d)", len(rep.ByType), rep.TypeBucketsTotal)
	}
	typeRows := make([][]string, 0, len(rep.ByType))
	for _, b := range rep.ByType {
		typeRows = append(typeRows, []string{b.Type, strconv.Itoa(b.Count)})
	}
	printMDSection(typeTitle, typeRows, []string{"Type", "Count"})
	return nil
}

func renderSummaryTable(rep summaryReport) error {
	scope := "customer-managed only"
	if rep.ManagedIncluded {
		scope = "incl. provider-managed"
	}
	header := fmt.Sprintf("Disco summary — %d resources (%s)", rep.Total, scope)
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

	acctRows := make([][2]string, 0, len(rep.ByAccount))
	for _, b := range rep.ByAccount {
		label := b.AccountID
		if b.AccountName != "" {
			label = b.AccountID + " (" + b.AccountName + ")"
		}
		acctRows = append(acctRows, [2]string{label, strconv.Itoa(b.Count)})
	}
	if len(acctRows) > 0 {
		printSection("BY ACCOUNT", acctRows)
	}

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
}

func init() {
	summaryCmd.Flags().StringSliceVarP(&summaryProviders, "providers", "p", nil, fmt.Sprintf("Filter by provider(s), comma-separated (%s)", providerListHint()))
	_ = summaryCmd.RegisterFlagCompletionFunc("providers", completeProviderNames)
	summaryCmd.Flags().StringSliceVarP(&summaryRegions, "regions", "r", nil, "Filter by region(s), comma-separated")
	summaryCmd.Flags().StringSliceVar(&summaryExcludeTypes, "exclude-types", nil, "Comma-separated resource types to exclude (e.g. aws:logs:log-stream)")
	summaryCmd.Flags().StringVar(&summaryScanID, "scan-id", "", "Restrict to one scan run; accepts a scan ID or 'latest'")
	summaryCmd.Flags().Var(&summaryDiscoveredSince, "discovered-since", "Restrict to rows first-seen by disco on or after this timestamp (RFC3339 or YYYY-MM-DD)")
	summaryCmd.Flags().Var(&summaryDiscoveredBefore, "discovered-before", "Restrict to rows first-seen by disco strictly before this timestamp (pairs with --discovered-since for half-open [since, before) intervals)")
	summaryCmd.Flags().Var(&summaryCreatedSince, "created-since", "Restrict to rows whose intrinsic CreateDate is on or after this timestamp (rows with no CreateDate are excluded)")
	summaryCmd.Flags().Var(&summaryCreatedBefore, "created-before", "Restrict to rows whose intrinsic CreateDate is strictly before this timestamp (rows with no CreateDate are excluded)")
	summaryCmd.Flags().StringVarP(&summaryOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = summaryCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))
	summaryCmd.Flags().IntVar(&summaryTopTypes, "top-types", 10, "Number of top resource types to show (0 = all)")
	summaryCmd.Flags().BoolVar(&summaryIncludeManaged, "include-managed", false, "Include provider-managed resources in the denominator")
	summaryCmd.Flags().BoolVar(&summarySkipGlobals, "exclude-global-region", false, "Exclude rows whose region is \"global\". By default --regions folds globals in.")
	summaryCmd.Flags().BoolVar(&summaryRequireResources, "require-resources", false, "Exit non-zero when 0 resources match (fail-closed gate against an empty / unscanned DB)")
	summaryCmd.Flags().Uint64Var(&summaryMinResources, "min-resources", 0, "Exit non-zero when fewer than N resources match (overrides --require-resources when both set)")
	rootCmd.AddCommand(summaryCmd)
}
