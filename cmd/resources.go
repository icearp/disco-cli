package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

// resourcesColumns is the column order for CSV output. The table renderer uses
// its own narrower header.
var resourcesColumns = []string{
	"provider", "account_id", "type", "name", "region", "status", "native_id",
	"id", "account_name", "zone", "managed_by_provider",
	"tags", "attributes",
	"created_at", "discovered_at", "discovered_by",
}

// resourcesMarkdownHeaders mirrors resourcesColumns positionally in Title Case, so
// `resources -o markdown` matches the Title Case headers every other markdown
// renderer uses (summary/scans/diff/findings/graph). Keep the two in lockstep.
var resourcesMarkdownHeaders = []string{
	"Provider", "Account ID", "Type", "Name", "Region", "Status", "Native ID",
	"ID", "Account Name", "Zone", "Managed By Provider",
	"Tags", "Attributes",
	"Created At", "Discovered At", "Discovered By",
}

// resourceRow returns the resource's column values in resourcesColumns order.
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
		s(r.CreatedAt), r.DiscoveredAt, r.DiscoveredBy,
	}
}

var (
	resourcesProviders        []string
	resourcesType             string
	resourcesExcludeTypes     []string
	resourcesRegions          []string
	resourcesStatus           string
	resourcesTagKey           string
	resourcesTagValue         string
	resourcesScanID           string
	resourcesID               string
	resourcesDiscoveredSince  = singleSetString{flag: "discovered-since"}
	resourcesDiscoveredBefore = singleSetString{flag: "discovered-before"}
	resourcesCreatedSince     = singleSetString{flag: "created-since"}
	resourcesCreatedBefore    = singleSetString{flag: "created-before"}
	resourcesOutputFmt        string
	resourcesLimit            uint64
	resourcesIncludeManaged   bool
	resourcesSkipGlobals      bool
	resourcesRequireResources bool
	resourcesMinResources     uint64
)

var resourcesCmd = &cobra.Command{
	Use:   "resources",
	Short: "List discovered resources",
	Args:  cobra.NoArgs,
	Long:  `List resources from the local database with optional filters.`,
	Example: `  disco resources
  disco resources --providers aws --type aws:ec2:instance
  disco resources --discovered-since 2026-01-01 -o jsonl | jq -s 'length'
  disco resources --created-before 2025-01-01 -t aws:iam:user --include-managed -o json
  disco resources --scan-id latest -o csv > q.csv
  disco resources --tag-key env --tag-value production -o json`,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(resourcesOutputFmt, rerr) }()
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		scanID, err := resolveScanID(db, resourcesScanID)
		if err != nil {
			return err
		}
		discoveredSince, err := parseTimeFlag("--discovered-since", resourcesDiscoveredSince.val)
		if err != nil {
			return err
		}
		discoveredBefore, err := parseTimeFlag("--discovered-before", resourcesDiscoveredBefore.val)
		if err != nil {
			return err
		}
		createdSince, err := parseTimeFlag("--created-since", resourcesCreatedSince.val)
		if err != nil {
			return err
		}
		createdBefore, err := parseTimeFlag("--created-before", resourcesCreatedBefore.val)
		if err != nil {
			return err
		}

		var types []string
		if resourcesType != "" {
			types = []string{resourcesType}
		}

		f := store.ResourceFilter{
			Providers:        resourcesProviders,
			Types:            types,
			ExcludeTypes:     resourcesExcludeTypes,
			Regions:          resourcesRegions,
			Status:           resourcesStatus,
			TagKey:           resourcesTagKey,
			TagValue:         resourcesTagValue,
			DiscoveredBy:     scanID,
			ID:               resourcesID,
			DiscoveredSince:  discoveredSince,
			DiscoveredBefore: discoveredBefore,
			CreatedSince:     createdSince,
			CreatedBefore:    createdBefore,
			Limit:            resourcesLimit,
			IncludeManaged:   resourcesIncludeManaged,
			SkipGlobals:      resourcesSkipGlobals,
		}

		// Non-nil contract for `[]` vs `null` JSON output is re-established
		// post-call (focus-group SUMMARY F6).
		var resources []store.Resource
		if resourcesLimit == 0 {
			resources, err = loadAllResourcesPaged(db, f)
		} else {
			// Fetch one extra row as a truncation probe: N+1 returned means more
			// matched than --limit allows; warn and trim. Equal-N no longer trips
			// a false positive.
			f.Limit = resourcesLimit + 1
			resources, err = db.ListResources(f)
			if err == nil && uint64(len(resources)) > resourcesLimit {
				resources = resources[:resourcesLimit]
				fmt.Fprintf(os.Stderr,
					"warning: --limit %d may be hiding rows; raise --limit or pass --limit 0 for all\n",
					resourcesLimit)
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

		if err := gateResourceCount(len(resources), resourcesRequireResources, resourcesMinResources); err != nil {
			return err
		}

		// When --scan-id returns no rows, the usual cause is the customer-only
		// filter dropping a managed resource the scan touched. Nudge on stderr
		// so the operator suspects the filter, not a disagreement with
		// `scans show`.
		if scanID != "" && !resourcesIncludeManaged && len(resources) == 0 {
			fmt.Fprintf(os.Stderr,
				"note: --scan-id + customer-only filter returned 0 rows; pass --include-managed to evaluate provider-managed resources the scan touched\n")
		}

		switch resourcesOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resources)
		case "jsonl":
			// Newline-delimited JSON: one resource per line, no indent — suited
			// to streaming into jq, log pipelines, or ELK.
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
			if err := w.Write(resourcesColumns); err != nil {
				return err
			}
			for _, r := range resources {
				if err := w.Write(resourceRow(&r)); err != nil {
					return err
				}
			}
			return nil
		case "markdown", "md":
			rows := make([][]string, 0, len(resources))
			for _, r := range resources {
				rows = append(rows, resourceRow(&r))
			}
			return renderMarkdownTable(os.Stdout, resourcesMarkdownHeaders, rows)
		case "table", "":
			if len(resources) == 0 {
				_, _ = fmt.Fprintln(os.Stderr, "No resources found.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "PROVIDER\tACCOUNT ID\tACCOUNT NAME\tRESOURCE TYPE\tNAME\tREGION\tSTATUS")
			for _, r := range resources {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					r.Provider, r.AccountID, ptrOrDash(r.AccountName), r.Type,
					ptrOrDash(r.Name), ptrOrDash(r.Region), ptrOrDash(r.Status))
			}
			return w.Flush()
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", resourcesOutputFmt)
		}
	},
}

var (
	resourcesShowProvider  string
	resourcesShowType      string
	resourcesShowAccount   string
	resourcesShowOutputFmt string
)

var resourcesShowCmd = &cobra.Command{
	Use:   "show <id|native-id|name>",
	Short: "Show a single resource",
	Args:  cobra.ExactArgs(1),
	Long: `Resolve one resource and print its full record. The argument resolves the
same way as 'disco graph' / 'disco history' — exact native-id or name, then a
32-hex resource-ID prefix, then a substring match. Pass --provider / --type /
--account to disambiguate when a seed matches more than one resource.`,
	Example: `  disco resources show i-0abc123
  disco resources show my-bucket --type aws:s3:bucket -o json
  disco resources show 9f3c --provider aws`,
	RunE: func(_ *cobra.Command, args []string) (rerr error) {
		defer func() { maybeStructuredError(resourcesShowOutputFmt, rerr) }()
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		r, err := db.ResolveResource(args[0], resourcesShowProvider, resourcesShowType, resourcesShowAccount)
		if err != nil {
			return err
		}

		switch resourcesShowOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(r)
		case "jsonl":
			return json.NewEncoder(os.Stdout).Encode(r)
		case "csv":
			w := csv.NewWriter(os.Stdout)
			defer w.Flush()
			if err := w.Write(resourcesColumns); err != nil {
				return err
			}
			return w.Write(resourceRow(r))
		case "markdown", "md":
			return renderMarkdownTable(os.Stdout, resourcesMarkdownHeaders, [][]string{resourceRow(r)})
		case "table", "":
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for i, col := range resourcesColumns {
				_, _ = fmt.Fprintf(w, "%s\t%s\n", col, resourceRow(r)[i])
			}
			return w.Flush()
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", resourcesShowOutputFmt)
		}
	},
}

func init() {
	resourcesShowCmd.Flags().StringVarP(&resourcesShowProvider, "provider", "p", "", "Disambiguate the seed by provider")
	_ = resourcesShowCmd.RegisterFlagCompletionFunc("provider", completeProviderNames)
	resourcesShowCmd.Flags().StringVarP(&resourcesShowType, "type", "t", "", "Disambiguate the seed by resource type (e.g. aws:ec2:instance)")
	resourcesShowCmd.Flags().StringVar(&resourcesShowAccount, "account", "", "Disambiguate the seed by account/subscription/project ID")
	resourcesShowCmd.Flags().StringVarP(&resourcesShowOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = resourcesShowCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))
	resourcesCmd.AddCommand(resourcesShowCmd)

	resourcesCmd.Flags().StringSliceVarP(&resourcesProviders, "providers", "p", nil, fmt.Sprintf("Filter by provider(s), comma-separated (%s)", providerListHint()))
	_ = resourcesCmd.RegisterFlagCompletionFunc("providers", completeProviderNames)
	resourcesCmd.Flags().StringVarP(&resourcesType, "type", "t", "", "Filter by resource type (e.g. aws:ec2:instance)")
	resourcesCmd.Flags().StringSliceVar(&resourcesExcludeTypes, "exclude-types", nil, "Comma-separated resource types to exclude (e.g. aws:logs:log-stream)")
	resourcesCmd.Flags().StringVar(&resourcesScanID, "scan-id", "", "Restrict to one scan run; accepts a scan ID or 'latest'")
	resourcesCmd.Flags().StringVar(&resourcesID, "id", "", "Lookup a single resource by primary-key ID (32-hex)")
	resourcesCmd.Flags().Var(&resourcesDiscoveredSince, "discovered-since", "Restrict to rows first-seen by disco on or after this timestamp (RFC3339 or YYYY-MM-DD)")
	resourcesCmd.Flags().Var(&resourcesDiscoveredBefore, "discovered-before", "Restrict to rows first-seen by disco strictly before this timestamp (pairs with --discovered-since for half-open [since, before) intervals)")
	resourcesCmd.Flags().Var(&resourcesCreatedSince, "created-since", "Restrict to rows whose intrinsic CreateDate is on or after this timestamp (rows with no CreateDate are excluded)")
	resourcesCmd.Flags().Var(&resourcesCreatedBefore, "created-before", "Restrict to rows whose intrinsic CreateDate is strictly before this timestamp (rows with no CreateDate are excluded)")
	resourcesCmd.Flags().StringSliceVarP(&resourcesRegions, "regions", "r", nil, "Filter by region(s), comma-separated")
	resourcesCmd.Flags().StringVar(&resourcesStatus, "status", "", "Filter by status")
	resourcesCmd.Flags().StringVar(&resourcesTagKey, "tag-key", "", "Filter by tag key (any value); composes with --tag-value as AND")
	resourcesCmd.Flags().StringVar(&resourcesTagValue, "tag-value", "", "Filter by tag value (matches any key when --tag-key is unset)")
	resourcesCmd.Flags().StringVarP(&resourcesOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = resourcesCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))
	resourcesCmd.Flags().Uint64Var(&resourcesLimit, "limit", 0, "Maximum number of results (0 = all; warning emitted on stderr if a positive --limit truncates)")
	resourcesCmd.Flags().BoolVar(&resourcesIncludeManaged, "include-managed", false, "Include provider-managed resources (built-in roles, AWS-owned prefix lists, etc.)")
	resourcesCmd.Flags().BoolVar(&resourcesSkipGlobals, "exclude-global-region", false, "Exclude rows whose region is \"global\" (IAM, Route53, CloudFront, tenant-scope Azure, org-scope GCP). By default --regions filters fold globals in.")
	resourcesCmd.Flags().BoolVar(&resourcesRequireResources, "require-resources", false, "Exit non-zero when 0 rows are returned (fail-closed gate against an empty / unscanned DB)")
	resourcesCmd.Flags().Uint64Var(&resourcesMinResources, "min-resources", 0, "Exit non-zero when fewer than N rows are returned (overrides --require-resources when both set)")
	rootCmd.AddCommand(resourcesCmd)
}
