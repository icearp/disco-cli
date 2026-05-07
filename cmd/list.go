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
	listProvider         string
	listType             string
	listExcludeTypes     []string
	listRegion           string
	listStatus           string
	listTagKey           string
	listTagValue         string
	listScanID           string
	listScanAs           string
	listID               string
	listDiscoveredSince  = singleSetString{flag: "discovered-since"}
	listDiscoveredBefore = singleSetString{flag: "discovered-before"}
	listCreatedSince     = singleSetString{flag: "created-since"}
	listCreatedBefore    = singleSetString{flag: "created-before"}
	listOutputFmt        string
	listLimit            uint64
	listIncludeManaged   bool
	listRequireResources bool
	listMinResources     uint64
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered resources",
	Args:  cobra.NoArgs,
	Long: `List resources from the local database with optional filters.

Examples:
  disco list
  disco list --provider aws --type aws:ec2:instance
  disco list --discovered-since 2026-01-01 -o jsonl | jq -s 'length'
  disco list --created-before 2025-01-01 -t aws:iam:user --include-managed -o json
  disco list --scan-id latest --scan-as discovered -o csv > q.csv
  disco list --tag-key env --tag-value production -o json`,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(listOutputFmt, rerr) }()
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		scanID, err := resolveScanID(db, listScanID)
		if err != nil {
			return err
		}
		discoveredSince, err := parseTimeFlag("--discovered-since", listDiscoveredSince.val)
		if err != nil {
			return err
		}
		discoveredBefore, err := parseTimeFlag("--discovered-before", listDiscoveredBefore.val)
		if err != nil {
			return err
		}
		createdSince, err := parseTimeFlag("--created-since", listCreatedSince.val)
		if err != nil {
			return err
		}
		createdBefore, err := parseTimeFlag("--created-before", listCreatedBefore.val)
		if err != nil {
			return err
		}

		var types []string
		if listType != "" {
			types = []string{listType}
		}
		var regions []string
		if listRegion != "" {
			regions = []string{listRegion}
		}

		switch listScanAs {
		case "", "any", "discovered", "verified":
		default:
			return fmt.Errorf("--scan-as must be discovered|verified|any (got %q)", listScanAs)
		}
		f := store.ResourceFilter{
			Provider:         listProvider,
			Types:            types,
			ExcludeTypes:     listExcludeTypes,
			Regions:          regions,
			Status:           listStatus,
			TagKey:           listTagKey,
			TagValue:         listTagValue,
			DiscoveredBy:     scanID,
			ScanAs:           listScanAs,
			ID:               listID,
			DiscoveredSince:  discoveredSince,
			DiscoveredBefore: discoveredBefore,
			CreatedSince:     createdSince,
			CreatedBefore:    createdBefore,
			Limit:            listLimit,
			IncludeManaged:   listIncludeManaged,
		}

		// Initialise to a non-nil empty slice so `-o json` on a zero-row
		// query emits `[]` instead of `null`. `null` was the #1 paper-cut
		// for downstream `jq` / Python pipelines (focus-group SUMMARY F6).
		resources := []store.Resource{}
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
		// Either branch above may reassign resources to a nil slice on a
		// zero-row query; re-establish the non-nil contract so json.Encode
		// emits `[]` not `null`.
		if resources == nil {
			resources = []store.Resource{}
		}

		if err := gateResourceCount(len(resources), listRequireResources, listMinResources); err != nil {
			return err
		}

		// When `--scan-as discovered` against a specific scan returns no rows,
		// the most common cause is the customer-only filter dropping a
		// managed resource the scan touched. Surface a stderr nudge so the
		// operator sees the filter as the suspect rather than reading a
		// disagreement with `scans show` as a bug.
		if scanID != "" && listScanAs == "discovered" && !listIncludeManaged && len(resources) == 0 {
			fmt.Fprintf(os.Stderr,
				"note: --scan-as discovered + customer-only filter returned 0 rows; pass --include-managed to evaluate provider-managed resources the scan touched\n")
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
			_, _ = fmt.Fprintln(w, "PROVIDER\tACCOUNT ID\tACCOUNT NAME\tRESOURCE TYPE\tNAME\tREGION\tSTATUS")
			for _, r := range resources {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					r.Provider, r.AccountID, ptrOrDash(r.AccountName), r.Type,
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
	listCmd.Flags().StringVar(&listScanID, "scan-id", "", "Restrict to one scan run; accepts a scan ID or 'latest'")
	listCmd.Flags().StringVar(&listScanAs, "scan-as", "any",
		"Treat --scan-id as the row's discovered | verified | any (default: any)")
	listCmd.Flags().StringVar(&listID, "id", "", "Lookup a single resource by primary-key ID (32-hex)")
	listCmd.Flags().Var(&listDiscoveredSince, "discovered-since", "Show rows first-seen by disco on or after this timestamp (RFC3339 or YYYY-MM-DD)")
	listCmd.Flags().Var(&listDiscoveredBefore, "discovered-before", "Show rows first-seen by disco strictly before this timestamp (pairs with --discovered-since for half-open [since, before) intervals)")
	listCmd.Flags().Var(&listCreatedSince, "created-since", "Show rows whose intrinsic CreateDate is on or after this timestamp (rows with no CreateDate are excluded)")
	listCmd.Flags().Var(&listCreatedBefore, "created-before", "Show rows whose intrinsic CreateDate is strictly before this timestamp (rows with no CreateDate are excluded)")
	listCmd.Flags().StringVarP(&listRegion, "region", "r", "", "Filter by region")
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status")
	listCmd.Flags().StringVar(&listTagKey, "tag-key", "", "Filter by tag key (any value); composes with --tag-value as AND")
	listCmd.Flags().StringVar(&listTagValue, "tag-value", "", "Filter by tag value (matches any key when --tag-key is unset)")
	listCmd.Flags().StringVarP(&listOutputFmt, "output", "o", "table", "Output format: table, json, jsonl, csv")
	listCmd.Flags().Uint64Var(&listLimit, "limit", 0, "Maximum number of results (0 = all; warning emitted on stderr if a positive --limit truncates)")
	listCmd.Flags().BoolVar(&listIncludeManaged, "include-managed", false, "Include provider-managed resources (built-in roles, AWS-owned prefix lists, etc.)")
	listCmd.Flags().BoolVar(&listRequireResources, "require-resources", false, "Exit non-zero when 0 rows are returned (fail-closed gate against an empty / unscanned DB)")
	listCmd.Flags().Uint64Var(&listMinResources, "min-resources", 0, "Exit non-zero when fewer than N rows are returned (overrides --require-resources when both set)")
	rootCmd.AddCommand(listCmd)
}
