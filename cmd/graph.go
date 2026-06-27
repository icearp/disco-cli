package cmd

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

var (
	graphProvider       string
	graphType           string
	graphAccount        string
	graphDepth          int
	graphPathDepth      int
	graphBlastDepth     int
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
	graphOrphansOnly    bool
)

// validRankdirs are the four DOT layout directions. LR (left-to-right) is
// the default; RL inverts horizontally — useful when edges in the DB are
// emitted child→parent (some hierarchy scanners do) and you want parent
// on the left visually. TB / BT give a vertical tree layout.
var validRankdirs = map[string]bool{"LR": true, "RL": true, "TB": true, "BT": true}

// graphOutputFormats is the set of values accepted by --output across all
// graph subcommands. Kept in one place so help text stays in sync.
var graphOutputFormats = []string{"table", "markdown", "csv", "json", "jsonl", "dot", "mermaid"}

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
  graph complete       dump every customer resource + connected managed`,
	Example: `  disco graph i-0abc123 --provider aws --depth 3
  disco graph my-bucket-name --type aws:s3:bucket
  disco graph <32-hex-id> --kinds contains,attached-to -o dot | dot -Tpng > g.png
  disco graph blast sg-0abc --depth 2          # what touches that SG?
  disco graph path web-1 db-1 -o dot           # shortest path between two resources
  disco graph complete --orphans-only          # disconnected resources only`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (rerr error) {
		defer func() { maybeStructuredError(graphOutputFmt, rerr) }()
		if len(args) == 0 {
			return cmd.Help()
		}
		db, err := openDB()
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

// graphPathCmd implements `disco graph path <from-id> <to-id>`.
var graphPathCmd = &cobra.Command{
	Use:   "path <from-id|name> <to-id|name>",
	Short: "Find the shortest path between two resources",
	Long: `Find the shortest edge sequence between two resource identifiers using
BFS over relationships. Honors --kinds / --direction / --exclude-types /
--exclude-regions / --include-managed. Default --depth for path is 8.

Returns exit code 1 (with no output) if the two resources are not connected
within the configured constraints.`,
	Example: `  disco graph path i-0abc123 sg-0def456
  disco graph path my-role my-bucket --kinds attached-to,uses -o mermaid`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) (rerr error) {
		defer func() {
			// Skip structured envelope on ErrNoPath: empty stdout + exit 1
			// is the documented contract for "no path", and pipelines key
			// off exit code rather than parsing a result document.
			if !errors.Is(rerr, store.ErrNoPath) {
				maybeStructuredError(graphOutputFmt, rerr)
			}
		}()
		db, err := openDB()
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

		g, err := db.GraphPath(from.ID, to.ID, store.GraphPathOpts{
			MaxDepth:       graphPathDepth,
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
				// Print a one-line stderr hint so interactive operators see
				// the retry shape; pipelines that discard stderr (or read
				// only stdout / $?) are unaffected — stdout stays empty,
				// exit code stays 1.
				kindsHint := "any"
				if len(graphKinds) > 0 {
					kindsHint = strings.Join(graphKinds, ",")
				}
				_, _ = fmt.Fprintf(os.Stderr,
					"no path between %s and %s within depth=%d, kinds=%s; retry with --depth %d or widen --kinds\n",
					args[0], args[1], graphPathDepth, kindsHint, graphPathDepth*2)
			}
			return err
		}
		return renderGraph(g, false)
	},
}

// graphBlastCmd implements `disco graph blast <id>`.
var graphBlastCmd = &cobra.Command{
	Use:   "blast <name|native-id|resource-id>",
	Short: "Compute outbound reachability (blast radius) from a seed",
	Long: `Walk all nodes reachable from the seed via outbound edges, grouping
results by distance ring. Default kind-set excludes 'contains' so hierarchy
fan-out does not dominate the radius. Default --depth for blast is 3.

IAM principals (users, roles, groups, service accounts) are destinations of
auth edges, not sources — a principal seed with --direction out (default)
returns the seed alone. blast detects this case and re-walks with
--direction both, noting the switch on stderr. Pin --direction out
explicitly to disable the fallback.

Caps via --max-nodes / --max-edges report truncation to stderr.`,
	Example: `  disco graph blast i-0abc123
  disco graph blast my-vpc --depth 5 --kinds contains,attached-to
  disco graph blast my-role --direction both -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (rerr error) {
		defer func() { maybeStructuredError(graphOutputFmt, rerr) }()
		db, err := openDB()
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
		dirSet := cmd.Parent().PersistentFlags().Changed("direction")
		kindsSet := cmd.Parent().PersistentFlags().Changed("kinds")
		opts := store.GraphWalkOpts{
			MaxDepth:       graphBlastDepth,
			Kinds:          kinds,
			Direction:      store.DirOut,
			IncludeManaged: graphIncludeManaged,
			ExcludeTypes:   graphExcludeTypes,
			ExcludeRegions: graphExcludeRegions,
			MaxNodes:       graphMaxNodes,
			MaxEdges:       graphMaxEdges,
		}
		g, err := db.GraphWalk(seed.ID, opts)
		if err != nil {
			return err
		}

		// Inbound-only seeds (IAM principals, target groups, VPCs, subnets,
		// any resource whose edges are emitted by other resolvers) leave a
		// DirOut walk seed-only. Re-walk DirBoth when the user did not pin
		// --direction. If --kinds was also left default, also include
		// 'contains' on the retry — closure edges (IAM principal → access
		// key, VPC → subnet) are 'contains' by schema.
		if !dirSet && len(g.Nodes) == 1 && len(g.Edges) == 0 {
			opts.Direction = store.DirBoth
			if !kindsSet {
				opts.Kinds = append(append([]string{}, kinds...), store.RelContains)
			}
			if g2, err2 := db.GraphWalk(seed.ID, opts); err2 == nil && (len(g2.Nodes) > 1 || len(g2.Edges) > 0) {
				fmt.Fprintln(os.Stderr,
					"note: seed has no outbound edges; expanded to --direction both "+
						"(some resources only receive edges — IAM principals, target groups, VPCs; "+
						"pass --direction out to disable)")
				g = g2
			}
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
	Example: `  disco graph complete
  disco graph complete --include-managed -o dot > graph.dot
  disco graph complete --orphans-only`,
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) (rerr error) {
		defer func() { maybeStructuredError(graphOutputFmt, rerr) }()
		db, err := openDB()
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
		if graphOrphansOnly {
			g = filterOrphans(g)
		}
		return renderGraph(g, false)
	},
}

// filterOrphans returns g with only the nodes that have zero in/out edges
// — surfaces dangling resources (unattached EBS volumes, key-pairs no
// instance uses, IAM users with no group/policy) for forensic / hygiene
// hunts. Keeps the result shape identical so renderers compose unchanged.
func filterOrphans(g *store.GraphResult) *store.GraphResult {
	hasEdge := make(map[string]bool, len(g.Nodes))
	for _, e := range g.Edges {
		hasEdge[e.FromID] = true
		hasEdge[e.ToID] = true
	}
	out := &store.GraphResult{
		SeedID:          g.SeedID,
		TruncatedNodes:  g.TruncatedNodes,
		TruncatedEdges:  g.TruncatedEdges,
		ExcludedTypes:   g.ExcludedTypes,
		ExcludedRegions: g.ExcludedRegions,
	}
	for _, n := range g.Nodes {
		if !hasEdge[n.Resource.ID] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	return out
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
	case "jsonl":
		// Single GraphResult on one unindented line, for shape parity with the
		// other jsonl emitters so `jq -c` consumes either without branching.
		return json.NewEncoder(os.Stdout).Encode(g)
	case "csv":
		return renderGraphCSV(g)
	case "markdown", "md":
		return renderGraphMarkdown(g)
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

// renderGraphCSV writes a single row-stream over both nodes and edges with
// a `kind` discriminator column so a graph result fits one CSV file. Mirror
// the existing `-o json` shape (nodes[] + edges[]) — consumers can split
// rows by `kind == "node"` vs `"edge"` to recover the two arrays.
func renderGraphCSV(g *store.GraphResult) error {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()
	if err := w.Write([]string{"kind", "depth", "id", "provider", "type", "name", "region", "from_id", "to_id", "edge_kind"}); err != nil {
		return err
	}
	for _, n := range g.Nodes {
		r := n.Resource
		if err := w.Write([]string{
			"node", strconv.Itoa(n.Depth), r.ID, r.Provider, r.Type,
			ptrOrEmpty(r.Name), ptrOrEmpty(r.Region), "", "", "",
		}); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		if err := w.Write([]string{
			"edge", "", "", "", "", "", "",
			e.FromID, e.ToID, e.Kind,
		}); err != nil {
			return err
		}
	}
	return nil
}

// renderGraphMarkdown writes one md doc with two sub-sections: a NODES
// table and an EDGES table. Both keyed via the same headers as the CSV
// shape, minus the `kind` discriminator since each section is homogeneous.
func renderGraphMarkdown(g *store.GraphResult) error {
	// `graph complete` has no seed; drop the seed token (and its double space).
	title := "# Graph"
	if g.SeedID != "" {
		title += " " + short(g.SeedID)
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s — %d nodes, %d edges\n\n", title, len(g.Nodes), len(g.Edges))

	if len(g.Nodes) > 0 {
		_, _ = fmt.Fprintln(os.Stdout, "## NODES")
		_, _ = fmt.Fprintln(os.Stdout)
		nodeRows := make([][]string, 0, len(g.Nodes))
		for _, n := range g.Nodes {
			r := n.Resource
			nodeRows = append(nodeRows, []string{
				strconv.Itoa(n.Depth), short(r.ID), r.Provider, r.Type,
				ptrOrEmpty(r.Name), ptrOrEmpty(r.Region),
			})
		}
		if err := renderMarkdownTable(os.Stdout,
			[]string{"Depth", "ID", "Provider", "Type", "Name", "Region"}, nodeRows); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(os.Stdout)
	}

	if len(g.Edges) > 0 {
		_, _ = fmt.Fprintln(os.Stdout, "## EDGES")
		_, _ = fmt.Fprintln(os.Stdout)
		edgeRows := make([][]string, 0, len(g.Edges))
		for _, e := range g.Edges {
			edgeRows = append(edgeRows, []string{short(e.FromID), e.Kind, short(e.ToID)})
		}
		if err := renderMarkdownTable(os.Stdout,
			[]string{"From", "Kind", "To"}, edgeRows); err != nil {
			return err
		}
	}
	return nil
}

// renderGraphTable prints a NODES section and an EDGES section, both using
// short 8-char ID prefixes to keep rows scannable.
func renderGraphTable(g *store.GraphResult) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	// `graph complete` has no seed; omit the empty "Seed:" line in that case.
	if g.SeedID != "" {
		_, _ = fmt.Fprintf(w, "Seed: %s\n", short(g.SeedID))
	}
	_, _ = fmt.Fprintf(w, "Nodes: %d, Edges: %d\n\n", len(g.Nodes), len(g.Edges))

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
// so big graphs stay readable.
func renderGraphDot(g *store.GraphResult) error {
	theme := themeByName(graphDotTheme)

	var b strings.Builder
	b.WriteString("digraph disco {\n")
	fmt.Fprintf(&b, "  rankdir=%s;\n", graphRankdir)

	// Theme header — emits only the blocks the theme populates. A theme with
	// empty Graph + EdgePresets emits no `graph [...]` / `edge [...]` lines;
	// fully-populated themes emit all three.
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
			// scannable. Palette-less themes skip the block.
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
	// the text alongside without breaking the route. Harmless for themes
	// that don't set splines.
	for _, e := range g.Edges {
		preset := theme.EdgePresets[e.Kind]
		// dir=back means "lay out tail→head reversed": swap endpoints so
		// Graphviz ranks the head on the left (under rankdir=LR) while
		// dir=back keeps the arrowhead at the original tail. Without the
		// swap, dir only re-renders arrows; rank still flows source→target.
		from, to := e.FromID, e.ToID
		if preset["dir"] == "back" {
			from, to = to, from
		}
		extra := renderAttrs(preset)
		if extra != "" {
			fmt.Fprintf(&b, "  %q -> %q [xlabel=%q, %s];\n", from, to, e.Kind, extra)
		} else {
			fmt.Fprintf(&b, "  %q -> %q [xlabel=%q];\n", from, to, e.Kind)
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
	graphCmd.Flags().IntVar(&graphDepth, "depth", 2, "Maximum BFS traversal depth (0 = seed only)")
	graphCmd.PersistentFlags().StringSliceVar(&graphKinds, "kinds", nil, "Comma-separated edge kinds to traverse (default: all kinds)")
	graphCmd.PersistentFlags().StringVar(&graphDirection, "direction", "both", "Edge direction: out, in, both")
	_ = graphCmd.RegisterFlagCompletionFunc("direction", staticCompletion("out", "in", "both"))
	graphCmd.PersistentFlags().StringVarP(&graphOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl, dot, mermaid")
	_ = graphCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl", "dot", "mermaid"))
	graphCmd.PersistentFlags().BoolVar(&graphIncludeManaged, "include-managed", false, "Expand BFS through provider-managed nodes (default: terminal — included only when directly linked)")
	graphCmd.PersistentFlags().StringSliceVar(&graphExcludeTypes, "exclude-types", nil, "Drop nodes whose type matches; literal or suffix-glob (e.g. 'aws:iam:*')")
	graphCmd.PersistentFlags().StringSliceVar(&graphExcludeRegions, "exclude-regions", nil, "Drop nodes whose region matches exactly")
	graphCmd.PersistentFlags().IntVar(&graphMaxNodes, "max-nodes", 0, "Cap nodes (0 = unlimited); BFS halts adding once hit, drops reported on stderr")
	graphCmd.PersistentFlags().IntVar(&graphMaxEdges, "max-edges", 0, "Cap edges (0 = unlimited)")
	graphCmd.PersistentFlags().StringVar(&graphCluster, "cluster", "", "Cluster nodes in dot/mermaid output by: provider, region, account")
	_ = graphCmd.RegisterFlagCompletionFunc("cluster", staticCompletion("provider", "region", "account"))
	graphCmd.PersistentFlags().StringVar(&graphLabelTemplate, "label-template", "", "text/template for dot/mermaid labels; fields: Name, Type, Provider, Account, Region, NativeID")
	graphCmd.PersistentFlags().StringVar(&graphDotTheme, "dot-theme", "light", "DOT styling theme: "+strings.Join(dotThemeNames(), ", "))
	_ = graphCmd.RegisterFlagCompletionFunc("dot-theme", staticCompletion(dotThemeNames()...))
	graphCmd.PersistentFlags().StringVar(&graphRankdir, "rankdir", "LR", "DOT layout direction: LR, RL, TB, BT (RL inverts horizontally — handy when edges flow child→parent)")
	_ = graphCmd.RegisterFlagCompletionFunc("rankdir", staticCompletion("LR", "RL", "TB", "BT"))
	graphPathCmd.Flags().IntVar(&graphPathDepth, "depth", 8, "Maximum BFS traversal depth (0 = seed only)")
	graphBlastCmd.Flags().IntVar(&graphBlastDepth, "depth", 3, "Maximum BFS traversal depth (0 = seed only)")
	graphCompleteCmd.Flags().BoolVar(&graphOrphansOnly, "orphans-only", false,
		"Keep only resources with zero in/out edges — surfaces dangling volumes, key-pairs, IAM principals, etc.")
	graphCmd.AddCommand(graphPathCmd, graphBlastCmd, graphCompleteCmd)
	rootCmd.AddCommand(graphCmd)
}
