package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

var diffOutputFmt string

var diffCmd = &cobra.Command{
	Use:   "diff <from-scan-id> <to-scan-id>",
	Short: "Show resource delta between two scans",
	Long: `Compare two scan runs and report resources added by the newer scan
and resources that are stale (last verified by the older scan).

Limitations: attribute drift (updated fields on the same resource) is not
reported — the schema stores only the latest state of each resource.

Examples:
  disco diff abc123 def456
  disco diff abc123 def456 -o json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		d, err := db.DiffScans(args[0], args[1])
		if err != nil {
			return err
		}

		switch diffOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(d)
		case "jsonl":
			return renderDiffJSONL(d)
		case "csv":
			return renderDiffCSV(d)
		case "markdown", "md":
			return renderDiffMarkdown(d)
		case "table", "":
			return renderDiffTable(d)
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", diffOutputFmt)
		}
	},
}

// renderDiffTable prints a two-section table: ADDED, then STALE.
func renderDiffTable(d *store.ScanDiff) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Diff %s → %s\n", d.FromScanID, d.ToScanID)
	_, _ = fmt.Fprintf(w, "Added: %d, Stale: %d\n\n", len(d.Added), len(d.Stale))

	if len(d.Added) > 0 {
		_, _ = fmt.Fprintln(w, "ADDED")
		_, _ = fmt.Fprintln(w, "STATE\tPROVIDER\tACCOUNT ID\tRESOURCE TYPE\tNAME\tREGION")
		for _, r := range d.Added {
			_, _ = fmt.Fprintf(w, "+\t%s\t%s\t%s\t%s\t%s\n",
				r.Provider, r.AccountID, r.Type, ptrOrDash(r.Name), ptrOrDash(r.Region))
		}
		_, _ = fmt.Fprintln(w)
	}
	if len(d.Stale) > 0 {
		_, _ = fmt.Fprintln(w, "STALE")
		_, _ = fmt.Fprintln(w, "STATE\tPROVIDER\tACCOUNT ID\tRESOURCE TYPE\tNAME\tREGION")
		for _, r := range d.Stale {
			_, _ = fmt.Fprintf(w, "-\t%s\t%s\t%s\t%s\t%s\n",
				r.Provider, r.AccountID, r.Type, ptrOrDash(r.Name), ptrOrDash(r.Region))
		}
	}
	return w.Flush()
}

// diffRowsCSV flattens added + stale lists into a single column-stable
// row stream tagged by `change_type` (added | stale). Columns positional
// per cmd/CLAUDE.md "csv columns are positional-stable" rule.
func diffRowsCSV(d *store.ScanDiff) [][]string {
	rows := make([][]string, 0, len(d.Added)+len(d.Stale))
	for _, r := range d.Added {
		rows = append(rows, []string{
			"added", r.Provider, r.AccountID, r.Type, ptrOrEmpty(r.Name), ptrOrEmpty(r.Region),
		})
	}
	for _, r := range d.Stale {
		rows = append(rows, []string{
			"stale", r.Provider, r.AccountID, r.Type, ptrOrEmpty(r.Name), ptrOrEmpty(r.Region),
		})
	}
	return rows
}

// diffCSVHeader returns the canonical header for the diff CSV / Markdown output.
func diffCSVHeader() []string {
	return []string{"change_type", "provider", "account_id", "resource_type", "name", "region"}
}

func renderDiffCSV(d *store.ScanDiff) error {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()
	if err := w.Write(diffCSVHeader()); err != nil {
		return err
	}
	for _, row := range diffRowsCSV(d) {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func renderDiffMarkdown(d *store.ScanDiff) error {
	headers := []string{"Change", "Provider", "Account", "Type", "Name", "Region"}
	return renderMarkdownTable(os.Stdout, headers, diffRowsCSV(d))
}

// renderDiffJSONL emits each added / stale entry as one JSON line tagged
// with a `change_type` discriminator. Suited to `disco diff … -o jsonl |
// jq -c '. | select(.change_type=="added")'` drift pipelines.
func renderDiffJSONL(d *store.ScanDiff) error {
	enc := json.NewEncoder(os.Stdout)
	type entry struct {
		ChangeType string         `json:"change_type"`
		Resource   store.Resource `json:"resource"`
	}
	for _, r := range d.Added {
		if err := enc.Encode(entry{ChangeType: "added", Resource: r}); err != nil {
			return err
		}
	}
	for _, r := range d.Stale {
		if err := enc.Encode(entry{ChangeType: "stale", Resource: r}); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	diffCmd.Flags().StringVarP(&diffOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	rootCmd.AddCommand(diffCmd)
}
