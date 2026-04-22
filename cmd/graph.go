package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var (
	graphProvider  string
	graphType      string
	graphAccount   string
	graphDepth     int
	graphKinds     []string
	graphDirection string
	graphOutputFmt string
)

var graphCmd = &cobra.Command{
	Use:   "graph <name|native-id|resource-id>",
	Short: "Walk the resource graph from a seed resource",
	Long: `Starting from a seed resource, walk the stored resource graph
(relationships edges) up to --depth hops, optionally filtered by edge kind
and direction.

The seed may be a resource name, a native ID (e.g. i-0abc123, bucket name,
project ID), or the opaque 32-hex-char resource ID. If the identifier is
ambiguous across providers, types, or accounts, pass --provider / --type /
--account to disambiguate.

Examples:
  disco graph i-0abc123 --provider aws --depth 3
  disco graph my-bucket-name --type aws:s3:bucket
  disco graph <32-hex-id> --kinds contains,attached-to -o dot | dot -Tpng > g.png`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := store.Open(defaultDBPath())
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		seed, err := db.ResolveResource(args[0], graphProvider, graphType, graphAccount)
		if err != nil {
			return err
		}

		g, err := db.GraphWalk(seed.ID, store.GraphWalkOpts{
			MaxDepth:  graphDepth,
			Kinds:     graphKinds,
			Direction: graphDirection,
		})
		if err != nil {
			return err
		}

		switch graphOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(g)
		case "dot":
			return renderGraphDot(g)
		case "table", "":
			return renderGraphTable(g)
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, json, dot)", graphOutputFmt)
		}
	},
}

// renderGraphTable prints a NODES section and an EDGES section, both using
// short 8-char ID prefixes to keep rows scannable.
func renderGraphTable(g *store.GraphResult) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Seed: %s\nNodes: %d, Edges: %d\n\n", short(g.SeedID), len(g.Nodes), len(g.Edges))

	_, _ = fmt.Fprintln(w, "NODES")
	_, _ = fmt.Fprintln(w, "DEPTH\tID\tPROVIDER\tTYPE\tNAME\tREGION")
	for _, n := range g.Nodes {
		r := n.Resource
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			n.Depth, short(r.ID), r.Provider, r.Type, ptrOrDash(r.Name), ptrOrDash(r.Region))
	}

	if len(g.Edges) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "EDGES")
		_, _ = fmt.Fprintln(w, "FROM\tKIND\tTO")
		for _, e := range g.Edges {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", short(e.FromID), e.Kind, short(e.ToID))
		}
	}
	return w.Flush()
}

// renderGraphDot emits a Graphviz digraph so users can pipe into `dot -Tpng`.
// Node labels combine type and name; edge labels carry the relationship kind.
func renderGraphDot(g *store.GraphResult) error {
	var b strings.Builder
	b.WriteString("digraph disco {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, fontname=\"Helvetica\"];\n")
	for _, n := range g.Nodes {
		r := n.Resource
		label := r.Type
		if r.Name != nil && *r.Name != "" {
			label = r.Type + `\n` + *r.Name
		}
		fmt.Fprintf(&b, "  %q [label=%q];\n", r.ID, label)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %q -> %q [label=%q];\n", e.FromID, e.ToID, e.Kind)
	}
	b.WriteString("}\n")
	_, err := os.Stdout.WriteString(b.String())
	return err
}

// short returns the first 8 chars of a resource ID for compact table display.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func init() {
	graphCmd.Flags().StringVar(&graphProvider, "provider", "", "Disambiguate native ID by provider")
	graphCmd.Flags().StringVar(&graphType, "type", "", "Disambiguate native ID by resource type")
	graphCmd.Flags().StringVar(&graphAccount, "account", "", "Disambiguate native ID by account/subscription/project")
	graphCmd.Flags().IntVar(&graphDepth, "depth", 2, "Maximum BFS traversal depth (0 = seed only)")
	graphCmd.Flags().StringSliceVar(&graphKinds, "kinds", nil, "Comma-separated edge kinds to traverse (default: all)")
	graphCmd.Flags().StringVar(&graphDirection, "direction", "both", "Edge direction: out, in, both")
	graphCmd.Flags().StringVarP(&graphOutputFmt, "output", "o", "table", "Output format: table, json, dot")
	rootCmd.AddCommand(graphCmd)
}
