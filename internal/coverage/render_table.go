package coverage

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// RenderTable writes the headline per provider, then a tabwriter-aligned
// row table. The bucket column supports shell filtering (`awk '$5 == "uncovered"'`).
func RenderTable(w io.Writer, matrices []Matrix) error {
	for _, m := range matrices {
		if _, err := fmt.Fprintf(w, "%s: %s\n", m.Provider, Headline(m)); err != nil {
			return err
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PROVIDER\tSERVICE\tKEY\tDISCO TYPE\tBUCKET\tDEPTH\tSCOPE\tREASON"); err != nil {
		return err
	}
	for _, m := range matrices {
		for _, r := range m.Rows {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", r.Provider, r.Service, dash(r.Key), dash(r.DiscoType), r.Bucket, r.Depth, dash(r.Scope), dash(r.Reason)); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
