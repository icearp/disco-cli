package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

var scansOutputFmt string

var scansCmd = &cobra.Command{
	Use:   "scans",
	Short: "List recorded scan runs",
	Args:  cobra.NoArgs,
	Long: `Lists every scan recorded in the local DB, newest first. 'disco scans'
itself takes no positional args — it always returns the full list. Use
'disco scans show <id|latest>' for a single-scan deep dive (envelope
shape documented under that subcommand's --help).

Pairs with 'disco list --scan-id <id>' to inspect rows produced by a
specific run; --scan-id accepts the same 8-31 char hex prefix or
'latest' shorthand that 'scans show' does.

Subcommands:
  disco scans show <id|latest>   full detail for one scan

The 'latest' shorthand resolves to the most-recent scan whose
resource_count > 0 (skips no-op re-verify runs); falls back to the
most-recent scan with a stderr note when none qualify.

The RESOURCES column is rows the scan touched (insert + re-verify), not
first-seen attribution. To split them, use:
  disco list --scan-id <id> --scan-as discovered   # rows the scan first saw
  disco list --scan-id <id> --scan-as verified     # rows the scan re-verified
  disco list --scan-id <id> --scan-as any          # both (default)

Examples:
  disco scans
  disco scans show latest
  disco scans -o json | jq '.[].id'`,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(scansOutputFmt, rerr) }()

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		scans, err := db.ListScans()
		if err != nil {
			return fmt.Errorf("list scans: %w", err)
		}
		return renderScans(scans, scansOutputFmt)
	},
}

var scansShowCmd = &cobra.Command{
	Use:   "show <id|latest>",
	Short: "Show full detail for one scan run",
	Long: `Print the full record for one scan: lifecycle timestamps, providers
included, scope (per-provider account/region/project filters captured at
scan time), arbitrary meta, the resource_count the scan touched, and the
disco binary version that ran it.

Accepts a full 32-hex scan ID, an 8–31 char hex prefix (matches the short
form 'disco scans' prints), or 'latest' (most-recent scan whose
resource_count > 0; falls back to the most-recent scan with a stderr note
when none qualify).

JSON envelope shape:
  {
    "id":             "<32-hex>",
    "started_at":     "<RFC3339>",
    "finished_at":    "<RFC3339 or null>",
    "status":         "running|completed|partial|failed",
    "providers":      ["aws", ...],
    "scope":          {<per-provider filter object>},
    "error":          "<string or null>",
    "resource_count": <int or null>,
    "meta":           {<arbitrary scan meta>}
  }

Examples:
  disco scans show latest
  disco scans show 29cdb173
  disco scans show latest -o json | jq '{id, status, resource_count}'`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) (rerr error) {
		defer func() { maybeStructuredError(scansOutputFmt, rerr) }()

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		id, err := resolveScanID(db, args[0])
		if err != nil {
			return err
		}
		sc, err := db.GetScan(id)
		if err != nil {
			return fmt.Errorf("get scan: %w", err)
		}
		return renderScanShow(sc, scansOutputFmt)
	},
}

func renderScans(scans []store.Scan, format string) error {
	// Re-establish the non-nil contract so `-o json` emits `[]` not `null`
	// on a zero-row query (mirrors list.go; F6 wire-contract parity).
	if scans == nil {
		scans = []store.Scan{}
	}
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(scans)
	case "jsonl":
		enc := json.NewEncoder(os.Stdout)
		for _, s := range scans {
			if err := enc.Encode(s); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		if err := w.Write([]string{"id", "started_at", "finished_at", "status", "providers", "resource_count"}); err != nil {
			return err
		}
		for _, s := range scans {
			if err := w.Write([]string{
				s.ID, s.StartedAt, ptrOrEmpty(s.FinishedAt), s.Status,
				strings.Join(s.Providers, ","), intPtrOrEmpty(s.ResourceCount),
			}); err != nil {
				return err
			}
		}
		return nil
	case "markdown", "md":
		rows := make([][]string, 0, len(scans))
		for _, s := range scans {
			rows = append(rows, []string{
				s.ID, s.StartedAt, ptrOrEmpty(s.FinishedAt), s.Status,
				strings.Join(s.Providers, ","), intPtrOrEmpty(s.ResourceCount),
			})
		}
		return renderMarkdownTable(os.Stdout, []string{"ID", "Started", "Finished", "Status", "Providers", "Resources"}, rows)
	case "table", "":
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tSTARTED\tFINISHED\tSTATUS\tPROVIDERS\tRESOURCES")
		for _, s := range scans {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				short(s.ID), s.StartedAt, ptrOrDash(s.FinishedAt), s.Status,
				strings.Join(s.Providers, ","), intPtrOrDash(s.ResourceCount))
		}
		return w.Flush()
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", format)
	}
}

func renderScanShow(sc *store.Scan, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sc)
	case "jsonl":
		// Single record as one line. Provided for shape parity with the parent
		// `disco scans -o jsonl` so consumers can pipe either form through
		// `jq -c` without branching.
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(sc)
	case "csv":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		if err := w.Write([]string{"id", "started_at", "finished_at", "status", "providers", "resource_count", "scope", "error"}); err != nil {
			return err
		}
		return w.Write([]string{
			sc.ID, sc.StartedAt, ptrOrEmpty(sc.FinishedAt), sc.Status,
			strings.Join(sc.Providers, ","), intPtrOrEmpty(sc.ResourceCount),
			sc.ScopeJSON, ptrOrEmpty(sc.Error),
		})
	case "markdown", "md":
		row := []string{
			sc.ID, sc.StartedAt, ptrOrEmpty(sc.FinishedAt), sc.Status,
			strings.Join(sc.Providers, ","), intPtrOrEmpty(sc.ResourceCount),
			sc.ScopeJSON, ptrOrEmpty(sc.Error),
		}
		return renderMarkdownTable(os.Stdout,
			[]string{"ID", "Started", "Finished", "Status", "Providers", "Resources", "Scope", "Error"},
			[][]string{row})
	case "table", "":
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintf(w, "ID:\t%s\n", sc.ID)
		_, _ = fmt.Fprintf(w, "Status:\t%s\n", sc.Status)
		_, _ = fmt.Fprintf(w, "Started:\t%s\n", sc.StartedAt)
		_, _ = fmt.Fprintf(w, "Finished:\t%s\n", ptrOrDash(sc.FinishedAt))
		_, _ = fmt.Fprintf(w, "Providers:\t%s\n", strings.Join(sc.Providers, ","))
		_, _ = fmt.Fprintf(w, "Resources:\t%s\n", intPtrOrDash(sc.ResourceCount))
		if sc.Error != nil && *sc.Error != "" {
			_, _ = fmt.Fprintf(w, "Error:\t%s\n", *sc.Error)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if sc.ScopeJSON != "" && sc.ScopeJSON != "{}" && sc.ScopeJSON != "null" {
			_, _ = fmt.Fprintln(os.Stdout, "Scope:")
			var pretty any
			if jerr := json.Unmarshal([]byte(sc.ScopeJSON), &pretty); jerr == nil {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("  ", "  ")
				_ = enc.Encode(pretty)
			} else {
				_, _ = fmt.Fprintf(os.Stdout, "  %s\n", sc.ScopeJSON)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", format)
	}
}

func intPtrOrEmpty(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func intPtrOrDash(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

func init() {
	scansCmd.PersistentFlags().StringVarP(&scansOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	scansCmd.AddCommand(scansShowCmd)
	rootCmd.AddCommand(scansCmd)
}
