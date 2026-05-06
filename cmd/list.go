package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

// listColumns is the canonical column order for CSV output. Pre-F7 columns
// keep their positions; chain-of-custody + full-fidelity columns appended
// so positional-index spreadsheet imports keep working. The table renderer
// uses its own narrower header.
var listColumns = []string{
	"provider", "account_id", "type", "name", "region", "status", "native_id",
	"id", "account_name", "zone", "managed_by_provider",
	"tags", "attributes",
	"created_at", "discovered_at", "discovered_by", "verified_at", "verified_by",
}

// resourceRow returns the resource's column values in listColumns order.
// Used by CSV output; nil string fields render as empty cells. tags and
// attributes carry the raw JSON blobs — encoding/csv quotes them as needed.
func resourceRow(r *store.Resource) []string {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	tags := ""
	if r.TagsJSON != nil {
		tags = *r.TagsJSON
	}
	return []string{
		r.Provider, r.AccountID, r.Type, s(r.Name), s(r.Region), s(r.Status), r.NativeID,
		r.ID, s(r.AccountName), s(r.Zone), strconv.FormatBool(r.ManagedByProvider),
		tags, r.AttributesJSON,
		s(r.CreatedAt), r.DiscoveredAt, r.DiscoveredBy, s(r.VerifiedAt), s(r.VerifiedBy),
	}
}

var (
	listProvider       string
	listType           string
	listExcludeTypes   []string
	listRegion         string
	listStatus         string
	listTagKey         string
	listTagValue       string
	listOutputFmt      string
	listLimit          uint64
	listIncludeManaged bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered resources",
	Long: `List resources from the local database with optional filters.

Examples:
  disco list
  disco list --provider aws --type aws:ec2:instance
  disco list --provider gcp --region us-central1 --status running
  disco list --tag-key env --tag-value production -o json`,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(listOutputFmt, rerr) }()
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		var types []string
		if listType != "" {
			types = []string{listType}
		}
		var regions []string
		if listRegion != "" {
			regions = []string{listRegion}
		}

		f := store.ResourceFilter{
			Provider:       listProvider,
			Types:          types,
			ExcludeTypes:   listExcludeTypes,
			Regions:        regions,
			Status:         listStatus,
			TagKey:         listTagKey,
			TagValue:       listTagValue,
			Limit:          listLimit,
			IncludeManaged: listIncludeManaged,
		}

		var resources []store.Resource
		if listLimit == 0 {
			resources, err = loadAllResourcesPaged(db, f)
		} else {
			// Fetch one extra row as a truncation probe. If the store
			// returns N+1, more matched than --limit allows; warn and trim.
			// Equal-N populations no longer trip a false positive.
			f.Limit = listLimit + 1
			resources, err = db.ListResources(f)
			if err == nil && uint64(len(resources)) > listLimit {
				resources = resources[:listLimit]
				fmt.Fprintf(os.Stderr,
					"warning: --limit %d may be hiding rows; raise --limit or pass --limit 0 for all\n",
					listLimit)
			}
		}
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}

		switch listOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resources)
		case "jsonl":
			// Newline-delimited JSON: one resource per line, no indent.
			// Suited to streaming into jq, log pipelines, or ELK.
			enc := json.NewEncoder(os.Stdout)
			for _, r := range resources {
				if err := enc.Encode(r); err != nil {
					return err
				}
			}
			return nil
		case "csv":
			w := csv.NewWriter(os.Stdout)
			defer w.Flush()
			if err := w.Write(listColumns); err != nil {
				return err
			}
			for _, r := range resources {
				if err := w.Write(resourceRow(&r)); err != nil {
					return err
				}
			}
			return nil
		case "table", "":
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "PROVIDER\tACCOUNT ID\tRESOURCE TYPE\tNAME\tREGION\tSTATUS")
			for _, r := range resources {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					r.Provider, r.AccountID, r.Type,
					ptrOrDash(r.Name), ptrOrDash(r.Region), ptrOrDash(r.Status))
			}
			return w.Flush()
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, json, jsonl, csv)", listOutputFmt)
		}
	},
}

func init() {
	listCmd.Flags().StringVarP(&listProvider, "provider", "p", "", "Filter by provider (aws, azure, gcp)")
	listCmd.Flags().StringVarP(&listType, "type", "t", "", "Filter by resource type (e.g. aws:ec2:instance)")
	listCmd.Flags().StringSliceVar(&listExcludeTypes, "exclude-types", nil, "Comma-separated resource types to exclude (e.g. aws:logs:log-stream)")
	listCmd.Flags().StringVarP(&listRegion, "region", "r", "", "Filter by region")
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status")
	listCmd.Flags().StringVar(&listTagKey, "tag-key", "", "Filter by tag key")
	listCmd.Flags().StringVar(&listTagValue, "tag-value", "", "Filter by tag value (requires --tag-key)")
	listCmd.Flags().StringVarP(&listOutputFmt, "output", "o", "table", "Output format: table, json, jsonl, csv")
	listCmd.Flags().Uint64Var(&listLimit, "limit", 0, "Maximum number of results (0 = all; warning emitted on stderr if a positive --limit truncates)")
	listCmd.Flags().BoolVar(&listIncludeManaged, "include-managed", false, "Include provider-managed resources (built-in roles, AWS-owned prefix lists, etc.)")
	rootCmd.AddCommand(listCmd)
}
