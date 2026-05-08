package coverage

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// RenderRegionsTable writes a tabwriter-aligned table of region rows.
func RenderRegionsTable(w io.Writer, rows []RegionRow) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PROVIDER\tREGION\tSTATUS"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Provider, r.Region, r.Status); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// RenderRegionsMarkdown writes a markdown table of region rows.
func RenderRegionsMarkdown(w io.Writer, rows []RegionRow) error {
	if _, err := fmt.Fprintln(w, "| Provider | Region | Status |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "|---|---|---|"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "| %s | %s | %s |\n", r.Provider, r.Region, r.Status); err != nil {
			return err
		}
	}
	return nil
}
