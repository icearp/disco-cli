package coverage

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// RenderTable writes a tabwriter-aligned plain-text matrix. Bucket column
// makes filtering by `awk '$4 == "covered"'` etc. straightforward at the
// shell.
func RenderTable(w io.Writer, matrices []Matrix) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PROVIDER\tSERVICE\tDISCO TYPE\tUPSTREAM KEY\tBUCKET"); err != nil {
		return err
	}
	for _, m := range matrices {
		for _, r := range m.Rows {
			disco := r.DiscoType
			if disco == "" {
				disco = "-"
			}
			up := r.UpstreamKey
			if up == "" {
				up = "-"
			}
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Provider, r.Service, disco, up, r.Bucket); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
}
