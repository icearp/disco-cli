package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var (
	graphProvider       string
	graphType           string
	graphAccount        string
	graphDepth          int
	graphKinds          []string
	graphDirection      string
	graphOutputFmt      string
	graphIncludeManaged bool
	graphExcludeTypes   []string
	graphExcludeRegions []string
	graphMaxNodes       int
	graphMaxEdges       int
	graphCluster        string
	graphLabelTemplate  string
	graphDotTheme       string
	graphRankdir        string
)

// validRankdirs are the four DOT layout directions. LR (left-to-right) is
// the default; RL inverts horizontally — useful when edges in the DB are
// emitted child→parent (some hierarchy scanners do) and you want parent
// on the left visually. TB / BT give a vertical tree layout.
var validRankdirs = map[string]bool{"LR": true, "RL": true, "TB": true, "BT": true}

// graphOutputFormats is the set of values accepted by --output across all
// graph subcommands. Kept in one place so help text stays in sync.
var graphOutputFormats = []string{"table", "json", "dot", "mermaid"}

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

Subcommands:
  graph path <A> <B>   shortest path between two resources
  graph blast <id>     outbound reachability with per-distance rings
  graph complete       dump every customer resource + connected managed

Examples:
  disco graph i-0abc123 --provider aws --depth 3
  disco graph my-bucket-name --type aws:s3:bucket
  disco graph <32-hex-id> --kinds contains,attached-to -o dot | dot -Tpng > g.png`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
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
			MaxDepth:       graphDepth,
			Kinds:          graphKinds,
			Direction:      graphDirection,
			IncludeManaged: graphIncludeManaged,
			ExcludeTypes:   graphExcludeTypes,
			ExcludeRegions: graphExcludeRegions,
			MaxNodes:       graphMaxNodes,
			MaxEdges:       graphMaxEdges,
		})
		if err != nil {
			return err
		}
		return renderGraph(g, false)
	},
}

// graphPathCmd implements `disco graph path <A> <B>`.
var graphPathCmd = &cobra.Command{
	Use:   "path <A> <B>",
	Short: "Shortest path between two resources",
	Long: `Find the shortest edge sequence between two resource identifiers using
BFS over relationships. Honors --kinds / --direction / --exclude-types /
--exclude-regions / --include-managed. Default --depth for path is 8.

Returns exit code 1 (with no output) if the two resources are not connected
within the configured constraints.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := store.Open(defaultDBPath())
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		from, err := db.ResolveResource(args[0], graphProvider, graphType, graphAccount)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", args[0], err)
		}
		to, err := db.ResolveResource(args[1], graphProvider, graphType, graphAccount)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", args[1], err)
		}

		// `graph` parent passes --depth=2 by default which is too tight for
		// path queries; the spec says default 8 unless the user explicitly
		// overrode the flag.
		depth := graphDepth
		if !cmd.Flags().Changed("depth") && !cmd.Parent().PersistentFlags().Changed("depth") {
			depth = 8
		}

		g, err := db.GraphPath(from.ID, to.ID, store.GraphPathOpts{
			MaxDepth:       depth,
			Kinds:          graphKinds,
			Direction:      graphDirection,
			IncludeManaged: graphIncludeManaged,
			ExcludeTypes:   graphExcludeTypes,
			ExcludeRegions: graphExcludeRegions,
		})
		if err != nil {
			if errors.Is(err, store.ErrNoPath) {
				// Silence cobra's usage + error printing so the shell sees a
				// clean exit code 1. Execute() also detects ErrNoPath and
				// suppresses the trailing error message.
				cmd.SilenceErrors = true
				cmd.SilenceUsage = true
			}
			return err
		}
		return renderGraph(g, false)
	},
}

// graphBlastCmd implements `disco graph blast <id>`.
var graphBlastCmd = &cobra.Command{
	Use:   "blast <id>",
	Short: "Outbound reachability (blast radius) from a seed",
	Long: `Walk all nodes reachable from the seed via outbound edges, grouping
results by distance ring. Default kind-set excludes 'contains' so hierarchy
fan-out does not dominate the radius. Default --depth for blast is 3.

Caps via --max-nodes / --max-edges report truncation to stderr.`,
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

		// Default kind-set: all relationship kinds except 'contains', so a
		// noun like "VPC" doesn't drag in every subnet/sg as blast targets.
		// User may override with --kinds.
		kinds := graphKinds
		if len(kinds) == 0 {
			kinds = []string{
				store.RelAttachedTo, store.RelUses, store.RelRoutesTo, store.RelPeer,
				store.RelAssumes, store.RelCrossAccountTrust, store.RelCrossSubRBAC,
				store.RelCrossProjectIAM,
			}
		}
		depth := graphDepth
		if !cmd.Flags().Changed("depth") && !cmd.Parent().PersistentFlags().Changed("depth") {
			depth = 3
		}

		g, err := db.GraphWalk(seed.ID, store.GraphWalkOpts{
			MaxDepth:       depth,
			Kinds:          kinds,
			Direction:      store.DirOut,
			IncludeManaged: graphIncludeManaged,
			ExcludeTypes:   graphExcludeTypes,
			ExcludeRegions: graphExcludeRegions,
			MaxNodes:       graphMaxNodes,
			MaxEdges:       graphMaxEdges,
		})
		if err != nil {
			return err
		}
		return renderGraph(g, true)
	},
}

// graphCompleteCmd implements `disco graph complete` — dump the entire
// stored graph in one shot. Customer-managed resources always included;
// provider-managed resources kept only when they share an edge with a
// customer resource (set --include-managed to keep orphan managed nodes too).
//
// Traversal flags (--depth/--kinds/--direction) are ignored since this is
// not a seeded walk; --exclude-types/--exclude-regions/--max-* honoured.
var graphCompleteCmd = &cobra.Command{
	Use:   "complete",
	Short: "Render the full discovered graph (all customer resources + connected managed)",
	Long: `Emit every resource in the store plus every relationship between them.

Provider-managed resources (e.g. AWS-managed IAM policies, Azure built-in
role definitions, GCP foreign-project stubs) are kept only when they have
at least one edge to a customer-managed resource — orphan managed nodes
drop out by default. Pass --include-managed to keep them all.

--depth, --kinds, and --direction are ignored (no seed, no BFS). Other
filter flags (--exclude-types, --exclude-regions, --max-nodes, --max-edges)
work as for the seeded subcommands.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := store.Open(defaultDBPath())
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		g, err := db.GraphAll(store.GraphAllOpts{
			IncludeManaged: graphIncludeManaged,
			ExcludeTypes:   graphExcludeTypes,
			ExcludeRegions: graphExcludeRegions,
			MaxNodes:       graphMaxNodes,
			MaxEdges:       graphMaxEdges,
		})
		if err != nil {
			return err
		}
		return renderGraph(g, false)
	},
}

// renderGraph dispatches on graphOutputFmt. blast=true switches the table
// renderer to its ring-grouped variant. Truncation totals are emitted to
// stderr regardless of format so they survive `-o json | jq`.
func renderGraph(g *store.GraphResult, blast bool) error {
	if g.TruncatedNodes > 0 || g.TruncatedEdges > 0 {
		fmt.Fprintf(os.Stderr, "truncated: %d nodes, %d edges (raise --max-nodes/--max-edges)\n",
			g.TruncatedNodes, g.TruncatedEdges)
	}
	switch graphOutputFmt {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(g)
	case "dot":
		if _, ok := themes[graphDotTheme]; !ok {
			return fmt.Errorf("unknown --dot-theme %q (supported: %s)", graphDotTheme, strings.Join(dotThemeNames(), ", "))
		}
		if !validRankdirs[graphRankdir] {
			return fmt.Errorf("unknown --rankdir %q (supported: LR, RL, TB, BT)", graphRankdir)
		}
		return renderGraphDot(g)
	case "mermaid":
		return renderGraphMermaid(g)
	case "table", "":
		if blast {
			return renderGraphBlastTable(g)
		}
		return renderGraphTable(g)
	default:
		return fmt.Errorf("unknown --output format %q (supported: %s)",
			graphOutputFmt, strings.Join(graphOutputFormats, ", "))
	}
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

// renderGraphBlastTable groups nodes into per-distance rings — the natural
// shape for blast-radius analysis. Edges section unchanged from explore mode.
func renderGraphBlastTable(g *store.GraphResult) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Seed: %s\nReachable: %d nodes, %d edges\n",
		short(g.SeedID), len(g.Nodes), len(g.Edges))

	// Bucket by depth so each ring renders contiguously.
	rings := map[int][]store.GraphNode{}
	maxDepth := 0
	for _, n := range g.Nodes {
		rings[n.Depth] = append(rings[n.Depth], n)
		if n.Depth > maxDepth {
			maxDepth = n.Depth
		}
	}
	for d := 0; d <= maxDepth; d++ {
		ns := rings[d]
		if len(ns) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "\nRing %d (%d):\n", d, len(ns))
		_, _ = fmt.Fprintln(w, "ID\tPROVIDER\tTYPE\tNAME\tREGION")
		for _, n := range ns {
			r := n.Resource
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				short(r.ID), r.Provider, r.Type, ptrOrDash(r.Name), ptrOrDash(r.Region))
		}
	}
	return w.Flush()
}

// nodeLabel renders a node's display label. If --label-template is set, the
// template is applied; otherwise the default "type\nname" shape is used.
func nodeLabel(r store.Resource) (string, error) {
	if graphLabelTemplate == "" {
		label := r.Type
		if r.Name != nil && *r.Name != "" {
			label = r.Type + "\n" + *r.Name
		}
		return label, nil
	}
	tpl, err := template.New("label").Parse(graphLabelTemplate)
	if err != nil {
		return "", fmt.Errorf("parse --label-template: %w", err)
	}
	ctx := map[string]string{
		"Name":     ptrOrEmpty(r.Name),
		"Type":     r.Type,
		"Provider": r.Provider,
		"Account":  r.AccountID,
		"Region":   ptrOrEmpty(r.Region),
		"NativeID": r.NativeID,
	}
	var b strings.Builder
	if err := tpl.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("execute --label-template: %w", err)
	}
	return b.String(), nil
}

// ptrOrEmpty mirrors ptrOrDash but returns "" instead of "-" — required for
// template execution where "-" would leak into rendered labels.
func ptrOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// clusterKey returns the value used by --cluster to bucket a resource into a
// dot/mermaid subgraph. Empty cluster value disables grouping.
func clusterKey(r store.Resource) string {
	switch graphCluster {
	case "provider":
		return r.Provider
	case "region":
		return ptrOrEmpty(r.Region)
	case "account":
		return r.AccountID
	default:
		return ""
	}
}

// renderGraphDot emits a Graphviz digraph. Styling lives in cmd/graph_theme.go;
// this function just walks nodes/edges and looks up preset attribute blocks.
// When --cluster is set, nodes are wrapped in `subgraph cluster_<key>` blocks
// so big graphs stay readable. When --dot-theme=mono, output is byte-for-byte
// identical to the pre-theme implementation for diff-stable piping.
func renderGraphDot(g *store.GraphResult) error {
	theme := themeByName(graphDotTheme)

	var b strings.Builder
	b.WriteString("digraph disco {\n")
	fmt.Fprintf(&b, "  rankdir=%s;\n", graphRankdir)

	// Theme header — emits only the blocks the theme populates. Mono
	// theme has empty Graph + EdgePresets so no `graph [...]` / `edge [...]`
	// lines appear; themed themes emit all three.
	if attrs := renderAttrs(theme.Graph); attrs != "" {
		fmt.Fprintf(&b, "  graph [%s];\n", attrs)
	}
	if attrs := renderAttrs(theme.NodeDefaults); attrs != "" {
		fmt.Fprintf(&b, "  node [%s];\n", attrs)
	}
	if attrs := renderAttrs(theme.EdgeDefaults); attrs != "" {
		fmt.Fprintf(&b, "  edge [%s];\n", attrs)
	}

	emitNode := func(indent string, n store.GraphNode) error {
		label, err := nodeLabel(n.Resource)
		if err != nil {
			return err
		}
		extra := renderAttrs(theme.NodePresets[presetForResource(&n.Resource)])
		if extra != "" {
			fmt.Fprintf(&b, "%s%q [label=%q, %s];\n", indent, n.Resource.ID, label, extra)
		} else {
			fmt.Fprintf(&b, "%s%q [label=%q];\n", indent, n.Resource.ID, label)
		}
		return nil
	}

	if graphCluster == "" {
		for _, n := range g.Nodes {
			if err := emitNode("  ", n); err != nil {
				return err
			}
		}
	} else {
		// Group nodes by cluster key, emit a subgraph block per cluster.
		groups := map[string][]store.GraphNode{}
		for _, n := range g.Nodes {
			groups[clusterKey(n.Resource)] = append(groups[clusterKey(n.Resource)], n)
		}
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			fmt.Fprintf(&b, "  subgraph cluster_%d {\n", i)
			fmt.Fprintf(&b, "    label=%q;\n", k)
			// Cluster styling rotates through the palette so adjacent
			// clusters never share a fill — keeps a 3+ cluster graph
			// scannable. Mono / palette-less themes skip the block.
			if len(theme.ClusterPalette) > 0 {
				cs := theme.ClusterPalette[i%len(theme.ClusterPalette)]
				fmt.Fprintf(&b, "    style=\"rounded,filled\";\n")
				fmt.Fprintf(&b, "    bgcolor=%q;\n", cs.BGColor)
				fmt.Fprintf(&b, "    color=%q;\n", cs.Border)
			}
			for _, n := range groups[k] {
				if err := emitNode("    ", n); err != nil {
					return err
				}
			}
			b.WriteString("  }\n")
		}
	}

	// xlabel (external label) — themed graphs use splines=ortho, which
	// drops standard edge labels with a Graphviz warning. xlabel floats
	// the text alongside without breaking the route. Mono uses xlabel
	// too — harmless when splines aren't set.
	for _, e := range g.Edges {
		extra := renderAttrs(theme.EdgePresets[e.Kind])
		if extra != "" {
			fmt.Fprintf(&b, "  %q -> %q [xlabel=%q, %s];\n", e.FromID, e.ToID, e.Kind, extra)
		} else {
			fmt.Fprintf(&b, "  %q -> %q [xlabel=%q];\n", e.FromID, e.ToID, e.Kind)
		}
	}
	b.WriteString("}\n")
	_, err := os.Stdout.WriteString(b.String())
	return err
}

// renderGraphMermaid emits a Mermaid flowchart for embedding in markdown
// (GitHub/GitLab/docs render natively; Slack does not). Node IDs are 8-char
// shortened so the source stays readable; full IDs round-trip through json.
func renderGraphMermaid(g *store.GraphResult) error {
	var b strings.Builder
	b.WriteString("flowchart LR\n")

	emitNode := func(indent string, n store.GraphNode) error {
		label, err := nodeLabel(n.Resource)
		if err != nil {
			return err
		}
		// Mermaid uses `\n` literal for line breaks inside the quoted label;
		// our default label template already emits that escape.
		fmt.Fprintf(&b, "%s%s[%q]\n", indent, mermaidNodeID(n.Resource.ID), label)
		return nil
	}

	if graphCluster == "" {
		for _, n := range g.Nodes {
			if err := emitNode("  ", n); err != nil {
				return err
			}
		}
	} else {
		groups := map[string][]store.GraphNode{}
		for _, n := range g.Nodes {
			groups[clusterKey(n.Resource)] = append(groups[clusterKey(n.Resource)], n)
		}
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			fmt.Fprintf(&b, "  subgraph c%d[%q]\n", i, k)
			for _, n := range groups[k] {
				if err := emitNode("    ", n); err != nil {
					return err
				}
			}
			b.WriteString("  end\n")
		}
	}

	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %s -- %q --> %s\n",
			mermaidNodeID(e.FromID), e.Kind, mermaidNodeID(e.ToID))
	}
	_, err := os.Stdout.WriteString(b.String())
	return err
}

// mermaidNodeID maps a 32-hex resource ID to a Mermaid-safe node identifier.
// Mermaid IDs cannot start with a digit cleanly in all renderers, so prefix
// with "n".
func mermaidNodeID(id string) string {
	return "n" + short(id)
}

func init() {
	graphCmd.PersistentFlags().StringVar(&graphProvider, "provider", "", "Disambiguate native ID by provider")
	graphCmd.PersistentFlags().StringVar(&graphType, "type", "", "Disambiguate native ID by resource type")
	graphCmd.PersistentFlags().StringVar(&graphAccount, "account", "", "Disambiguate native ID by account/subscription/project")
	graphCmd.PersistentFlags().IntVar(&graphDepth, "depth", 2, "Maximum BFS traversal depth (0 = seed only; defaults: explore=2, path=8, blast=3)")
	graphCmd.PersistentFlags().StringSliceVar(&graphKinds, "kinds", nil, "Comma-separated edge kinds to traverse (default: all; blast: all except 'contains')")
	graphCmd.PersistentFlags().StringVar(&graphDirection, "direction", "both", "Edge direction: out, in, both")
	graphCmd.PersistentFlags().StringVarP(&graphOutputFmt, "output", "o", "table", "Output format: table, json, dot, mermaid")
	graphCmd.PersistentFlags().BoolVar(&graphIncludeManaged, "include-managed", false, "Expand BFS through provider-managed nodes (default: terminal — included only when directly linked)")
	graphCmd.PersistentFlags().StringSliceVar(&graphExcludeTypes, "exclude-types", nil, "Drop nodes whose type matches; literal or suffix-glob (e.g. 'aws:iam:*')")
	graphCmd.PersistentFlags().StringSliceVar(&graphExcludeRegions, "exclude-regions", nil, "Drop nodes whose region matches exactly")
	graphCmd.PersistentFlags().IntVar(&graphMaxNodes, "max-nodes", 0, "Cap nodes (0 = unlimited); BFS halts adding once hit, drops reported on stderr")
	graphCmd.PersistentFlags().IntVar(&graphMaxEdges, "max-edges", 0, "Cap edges (0 = unlimited)")
	graphCmd.PersistentFlags().StringVar(&graphCluster, "cluster", "", "Cluster nodes in dot/mermaid output by: provider, region, account")
	graphCmd.PersistentFlags().StringVar(&graphLabelTemplate, "label-template", "", "text/template for dot/mermaid labels; fields: Name, Type, Provider, Account, Region, NativeID")
	graphCmd.PersistentFlags().StringVar(&graphDotTheme, "dot-theme", "light", "DOT styling theme: "+strings.Join(dotThemeNames(), ", ")+" (mono = byte-stable legacy output)")
	graphCmd.PersistentFlags().StringVar(&graphRankdir, "rankdir", "LR", "DOT layout direction: LR, RL, TB, BT (RL inverts horizontally — handy when edges flow child→parent)")
	graphCmd.AddCommand(graphPathCmd, graphBlastCmd, graphCompleteCmd)
	rootCmd.AddCommand(graphCmd)
}
