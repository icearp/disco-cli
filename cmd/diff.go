package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/store"
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
		db, err := store.Open(defaultDBPath())
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
		default:
			return renderDiffTable(d)
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

// ptrOrDash returns the pointed-to string, or "-" if the pointer is nil.
func ptrOrDash(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

func init() {
	diffCmd.Flags().StringVarP(&diffOutputFmt, "output", "o", "table", "Output format: table, json")
	rootCmd.AddCommand(diffCmd)
}
