package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var scansOutputFmt string

var scansCmd = &cobra.Command{
	Use:   "scans",
	Short: "List recorded scan runs",
	Args:  cobra.NoArgs,
	Long: `Lists every scan recorded in the local DB, newest first. Pairs with
'disco list --scan-id <id>' to inspect rows produced by a specific run.

Subcommands:
  disco scans show <id|latest>   full detail for one scan

The 'latest' shorthand resolves to the most-recent scan, regardless of status.

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
	Short: "Show full detail for one scan",
	Args:  cobra.ExactArgs(1),
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
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(scans)
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
		return fmt.Errorf("unknown --output format %q (supported: table, json, csv)", format)
	}
}

func renderScanShow(sc *store.Scan, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
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
		return fmt.Errorf("unknown --output format %q (supported: table, json, csv)", format)
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
	scansCmd.PersistentFlags().StringVarP(&scansOutputFmt, "output", "o", "table", "Output format: table, json, csv")
	scansCmd.AddCommand(scansShowCmd)
	rootCmd.AddCommand(scansCmd)
}
