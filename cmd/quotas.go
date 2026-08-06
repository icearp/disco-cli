package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/icearp/disco-cli/store"
	"github.com/spf13/cobra"
)

// quotasColumns is the column order for CSV output. The table renderer uses its
// own narrower header.
var quotasColumns = []string{
	"provider", "account_id", "region", "service_code", "quota_code", "dimension_key", "name",
	"value", "default_value", "unit", "adjustable",
	"period_unit", "period_value", "resource_type", "availability_zone", "sub_account_type",
	"id", "account_name", "service_name", "description", "attributes",
	"discovered_at", "discovered_by",
}

// quotasMarkdownHeaders mirrors quotasColumns positionally in Title Case, so
// `quotas -o markdown` matches the headers every other markdown renderer uses.
// Keep the two in lockstep.
var quotasMarkdownHeaders = []string{
	"Provider", "Account ID", "Region", "Service Code", "Quota Code", "Dimension Key", "Name",
	"Value", "Default Value", "Unit", "Adjustable",
	"Period Unit", "Period Value", "Resource Type", "Availability Zone", "Sub Account Type",
	"ID", "Account Name", "Service Name", "Description", "Attributes",
	"Discovered At", "Discovered By",
}

// quotaRow returns the quota's column values in quotasColumns order. nil
// fields render as empty cells rather than as a zero, because an unreported
// limit and a limit of zero are different facts.
func quotaRow(q *store.Quota) []string {
	return []string{
		q.Provider, q.AccountID, q.Region, q.ServiceCode, q.QuotaCode, q.DimensionKey, q.Name,
		formatQuotaValue(q.Value), formatQuotaValue(q.DefaultValue), derefOr(q.Unit, ""),
		strconv.FormatBool(q.Adjustable),
		derefOr(q.PeriodUnit, ""), formatQuotaPeriodValue(q.PeriodValue),
		derefOr(q.ResourceType, ""), derefOr(q.AvailabilityZone, ""), derefOr(q.SubAccountType, ""),
		q.ID, derefOr(q.AccountName, ""), derefOr(q.ServiceName, ""), derefOr(q.Description, ""), q.AttributesJSON,
		q.DiscoveredAt, q.DiscoveredBy,
	}
}

// formatQuotaPeriodValue renders a rate-window length, empty when the limit is
// a count rather than a rate. Empty rather than "0" for the same reason
// formatQuotaValue is: no window and a window of zero are different facts.
func formatQuotaPeriodValue(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// formatQuotaValue renders a limit without a trailing ".0" for whole numbers,
// which nearly all of them are. An absent limit renders empty, never "0".
func formatQuotaValue(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

var (
	quotasProviders    []string
	quotasAccounts     []string
	quotasRegions      []string
	quotasServiceCodes []string
	quotasAdjustable   string
	quotasRaisedOnly   bool
	quotasChangedOnly  bool
	quotasOutputFmt    string
	quotasLimit        int
)

var quotasCmd = &cobra.Command{
	Use:   "quotas",
	Short: "List recorded service quota limits",
	Args:  cobra.NoArgs,
	Long: `List service quota limits from the local database with optional filters.

Quotas are limits the provider enforces on an account, not things you
provisioned, so they live in their own table rather than alongside resources.
Each limit keeps a version chain: re-scanning records a new version only when
the value actually changes.

The most interesting filter is --adjustable=false. A non-adjustable limit moves
only when the provider moves it, with no request from you and no notification,
so its history is the only record that it happened. Combine it with --changed to
list exactly the hard ceilings the provider has moved.

AWS quotas are only recorded when a scan is run with --include-service-quotas.
Azure quotas are recorded on every scan.`,
	Example: `  disco quotas
  disco quotas --service ec2 --region us-east-1
  disco quotas --adjustable=false
  disco quotas --changed --adjustable=false
  disco quotas --raised -o json`,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(quotasOutputFmt, rerr) }()
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		adjustable, err := parseTristateFlag("--adjustable", quotasAdjustable)
		if err != nil {
			return err
		}

		quotas, err := db.ListQuotas(store.QuotaFilter{
			Providers:    quotasProviders,
			AccountIDs:   quotasAccounts,
			Regions:      quotasRegions,
			ServiceCodes: quotasServiceCodes,
			Adjustable:   adjustable,
			RaisedOnly:   quotasRaisedOnly,
			ChangedOnly:  quotasChangedOnly,
			Limit:        quotasLimit,
		})
		if err != nil {
			return fmt.Errorf("list quotas: %w", err)
		}
		// Re-establish the non-nil contract so json.Encode emits `[]` not `null`.
		if quotas == nil {
			quotas = []store.Quota{}
		}

		switch quotasOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(quotas)
		case "jsonl":
			enc := json.NewEncoder(os.Stdout)
			for _, q := range quotas {
				if err := enc.Encode(q); err != nil {
					return err
				}
			}
			return nil
		case "csv":
			w := csv.NewWriter(os.Stdout)
			defer w.Flush()
			if err := w.Write(quotasColumns); err != nil {
				return err
			}
			for _, q := range quotas {
				if err := w.Write(quotaRow(&q)); err != nil {
					return err
				}
			}
			return nil
		case "markdown", "md":
			rows := make([][]string, 0, len(quotas))
			for _, q := range quotas {
				rows = append(rows, quotaRow(&q))
			}
			return renderMarkdownTable(os.Stdout, quotasMarkdownHeaders, rows)
		case "table", "":
			if len(quotas) == 0 {
				_, _ = fmt.Fprintln(os.Stderr, "No quotas found. AWS quotas need a scan with --include-service-quotas.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "PROVIDER\tACCOUNT ID\tREGION\tSERVICE\tQUOTA\tVALUE\tDEFAULT\tUNIT\tADJUSTABLE")
			for _, q := range quotas {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%t\n",
					q.Provider, q.AccountID, q.Region, q.ServiceCode, q.Name,
					dashIfEmpty(formatQuotaValue(q.Value)), dashIfEmpty(formatQuotaValue(q.DefaultValue)),
					ptrOrDash(q.Unit), q.Adjustable)
			}
			return w.Flush()
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", quotasOutputFmt)
		}
	},
}

// quotaHistoryColumns is the canonical column order for a quota's version
// history. It differs from historyColumns because the interesting change in a
// quota chain is the VALUE, which a resource-shaped view has nowhere to put.
var quotaHistoryColumns = []string{
	"version", "id", "service_code", "quota_code", "region",
	"value", "default_value", "unit", "adjustable",
	"discovered_at", "verified_at", "verified_by", "current",
}

// quotaHistoryEntry is the timeline shape for one version of a quota. Like
// historyEntry it is purpose-built rather than store.QuotaVersion, because
// store.Quota carries a value-receiver MarshalJSON that would be promoted onto
// an embedding struct and silently drop the version fields from JSON output.
type quotaHistoryEntry struct {
	Version      int      `json:"version"`
	ID           string   `json:"id"`
	ServiceCode  string   `json:"serviceCode"`
	QuotaCode    string   `json:"quotaCode"`
	Region       string   `json:"region"`
	Value        *float64 `json:"value"`
	DefaultValue *float64 `json:"defaultValue"`
	Unit         *string  `json:"unit"`
	Adjustable   bool     `json:"adjustable"`
	DiscoveredAt string   `json:"discoveredAt"`
	VerifiedAt   *string  `json:"verifiedAt"`
	VerifiedBy   *string  `json:"verifiedBy"`
	Current      bool     `json:"current"`
}

func quotaHistoryEntries(versions []store.QuotaVersion) []quotaHistoryEntry {
	out := make([]quotaHistoryEntry, 0, len(versions))
	for i, v := range versions {
		out = append(out, quotaHistoryEntry{
			// Chain order is root-first; GetQuotaVersions tiebreaks on the
			// UUIDv7 row id, since discovered_at is inherited identically
			// across a chain and carries no ordering signal.
			Version:      i + 1,
			ID:           v.RootID,
			ServiceCode:  v.ServiceCode,
			QuotaCode:    v.QuotaCode,
			Region:       v.Region,
			Value:        v.Value,
			DefaultValue: v.DefaultValue,
			Unit:         v.Unit,
			Adjustable:   v.Adjustable,
			DiscoveredAt: v.DiscoveredAt,
			VerifiedAt:   v.VerifiedAt,
			VerifiedBy:   v.VerifiedBy,
			Current:      v.SupersededBy == nil,
		})
	}
	return out
}

func quotaHistoryRow(e quotaHistoryEntry) []string {
	return []string{
		strconv.Itoa(e.Version), e.ID, e.ServiceCode, e.QuotaCode, e.Region,
		formatQuotaValue(e.Value), formatQuotaValue(e.DefaultValue), derefOr(e.Unit, ""),
		strconv.FormatBool(e.Adjustable),
		e.DiscoveredAt, derefOr(e.VerifiedAt, ""), derefOr(e.VerifiedBy, ""),
		strconv.FormatBool(e.Current),
	}
}

// renderQuotaHistory prints a quota's version chain in the requested format.
// Called from `disco history` when the argument resolves to a quota.
func renderQuotaHistory(versions []store.QuotaVersion, format string) error {
	entries := quotaHistoryEntries(versions)
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	case "jsonl":
		enc := json.NewEncoder(os.Stdout)
		for _, e := range entries {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		if err := w.Write(quotaHistoryColumns); err != nil {
			return err
		}
		for _, e := range entries {
			if err := w.Write(quotaHistoryRow(e)); err != nil {
				return err
			}
		}
		return nil
	case "markdown", "md":
		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, quotaHistoryRow(e))
		}
		return renderMarkdownTable(os.Stdout, quotaHistoryColumns, rows)
	case "table", "":
		if len(entries) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "No version history found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "VERSION\tDISCOVERED AT\tVERIFIED AT\tVALUE\tDEFAULT\tUNIT\tADJUSTABLE\tCURRENT")
		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%t\t%t\n",
				e.Version, e.DiscoveredAt, ptrOrDash(e.VerifiedAt),
				dashIfEmpty(formatQuotaValue(e.Value)), dashIfEmpty(formatQuotaValue(e.DefaultValue)),
				ptrOrDash(e.Unit), e.Adjustable, e.Current)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", format)
	}
}

// parseTristateFlag reads a string flag that means unset / true / false, so a
// boolean filter can distinguish "not filtering" from "filtering on false".
// A plain bool flag cannot: its zero value and an explicit false are the same
// value, which would silently turn `disco quotas` into `--adjustable=false`.
func parseTristateFlag(name, raw string) (*bool, error) {
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %q is not a boolean (use true or false)", name, raw)
	}
	return &v, nil
}

func init() {
	f := quotasCmd.Flags()
	f.StringSliceVar(&quotasProviders, "providers", nil, "Filter by provider (aws, azure, gcp)")
	f.StringSliceVar(&quotasAccounts, "accounts", nil, "Filter by account, subscription or project id")
	f.StringSliceVar(&quotasRegions, "regions", nil, "Filter by region (use 'global' for partition-wide limits)")
	f.StringSliceVar(&quotasServiceCodes, "service", nil, "Filter by service code (AWS ServiceCode, Azure provider namespace)")
	f.StringVar(&quotasAdjustable, "adjustable", "", "Filter by whether the limit can be raised on request: true or false")
	f.BoolVar(&quotasRaisedOnly, "raised", false, "Only limits whose value differs from the provider default")
	f.BoolVar(&quotasChangedOnly, "changed", false, "Only limits that have held more than one value")
	f.IntVar(&quotasLimit, "limit", 0, "Maximum rows to return (0 = all)")
	f.StringVarP(&quotasOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = quotasCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))
	_ = quotasCmd.RegisterFlagCompletionFunc("adjustable", staticCompletion("true", "false"))
	rootCmd.AddCommand(quotasCmd)
}
