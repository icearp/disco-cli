package store

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Direction constants for GraphWalkOpts.
const (
	DirOut  = "out"
	DirIn   = "in"
	DirBoth = "both"
)

// ErrNoPath signals GraphPath found no route to the destination within the
// configured depth/filter constraints. cmd layer maps this to a nonzero exit
// code so shell pipelines can branch on reachability.
var ErrNoPath = errors.New("no path between resources")

// GraphNode is a resource plus the BFS depth at which it was first reached.
type GraphNode struct {
	Resource Resource `json:"resource"`
	Depth    int      `json:"depth"`
}

// GraphEdge is a directed relationship between two resource IDs.
type GraphEdge struct {
	FromID string `json:"fromId"`
	ToID   string `json:"toId"`
	Kind   string `json:"kind"`
}

// GraphResult is GraphWalk's output: seed ID plus ordered nodes and edges.
// Truncated{Nodes,Edges} count candidates dropped by the caller's
// MaxNodes/MaxEdges caps; zero when no cap or no truncation.
type GraphResult struct {
	SeedID          string      `json:"seedId"`
	Nodes           []GraphNode `json:"nodes"`
	Edges           []GraphEdge `json:"edges"`
	TruncatedNodes  int         `json:"truncatedNodes,omitempty"`
	TruncatedEdges  int         `json:"truncatedEdges,omitempty"`
	ExcludedTypes   int         `json:"excludedTypes,omitempty"`   // dropped by ExcludeTypes
	ExcludedRegions int         `json:"excludedRegions,omitempty"` // dropped by ExcludeRegions
}

// GraphWalkOpts configures GraphWalk traversal.
type GraphWalkOpts struct {
	MaxDepth  int      // inclusive; 0 = seed only
	Kinds     []string // empty = all relationship kinds
	Direction string   // "out", "in", "both" (default: "both")
	// IncludeManaged=false treats provider-managed resources as terminal: they
	// appear as edge endpoints when reached from a non-managed node, but BFS
	// does not expand through them. The seed itself is never filtered.
	IncludeManaged bool
	// ExcludeTypes drops nodes whose Type matches any pattern. Patterns are
	// either literal ("aws:iam:role") or suffix-glob ("aws:iam:*"). The seed
	// is never excluded by this filter.
	ExcludeTypes []string
	// ExcludeRegions drops nodes whose Region matches any entry exactly.
	// Resources without a region (global services) are never matched.
	// The seed is never excluded by this filter.
	ExcludeRegions []string
	// MaxNodes / MaxEdges cap the result size. BFS halts adding new nodes /
	// edges once the cap is hit; the cumulative drop count is returned in
	// GraphResult.Truncated{Nodes,Edges}. 0 means unlimited.
	MaxNodes int
	MaxEdges int
}

// GraphWalk does a bounded BFS starting at seedID, following edges in the
// requested direction(s) filtered by kind, and returns the reachable subgraph.
func (s *Store) GraphWalk(seedID string, opts GraphWalkOpts) (*GraphResult, error) {
	seed, err := s.GetResource(seedID)
	if err != nil {
		return nil, err
	}
	dir := opts.Direction
	if dir == "" {
		dir = DirBoth
	}
	if dir != DirOut && dir != DirIn && dir != DirBoth {
		return nil, fmt.Errorf("invalid direction %q (want out|in|both)", dir)
	}
	if opts.MaxDepth < 0 {
		return nil, fmt.Errorf("invalid max depth %d", opts.MaxDepth)
	}

	result := &GraphResult{SeedID: seedID}
	visited := map[string]int{seedID: 0}
	result.Nodes = append(result.Nodes, GraphNode{Resource: *seed, Depth: 0})

	// Dedup edges so undirected peer links or double-resolved relationships
	// only appear once in the result.
	edgeKey := func(e GraphEdge) string { return e.FromID + "|" + e.Kind + "|" + e.ToID }
	seenEdge := map[string]struct{}{}

	frontier := []string{seedID}
	for depth := 0; depth < opts.MaxDepth && len(frontier) > 0; depth++ {
		candidates, next, err := s.collectFrontierEdges(frontier, dir, opts.Kinds, visited)
		if err != nil {
			return nil, err
		}

		newIDs := make([]string, 0, len(next))
		for id := range next {
			newIDs = append(newIDs, id)
		}
		newRes, err := fetchResourcesByIDs(s, newIDs)
		if err != nil {
			return nil, err
		}
		newResByID := make(map[string]Resource, len(newRes))
		for _, r := range newRes {
			newResByID[r.ID] = r
		}

		excluded := classifyExcluded(newResByID, seedID, opts, result)

		// Edges: drop ones whose endpoint is excluded; respect MaxEdges cap.
		for _, e := range candidates {
			if excluded[e.FromID] || excluded[e.ToID] {
				continue
			}
			if _, ok := seenEdge[edgeKey(e)]; ok {
				continue
			}
			if opts.MaxEdges > 0 && len(result.Edges) >= opts.MaxEdges {
				result.TruncatedEdges++
				continue
			}
			seenEdge[edgeKey(e)] = struct{}{}
			result.Edges = append(result.Edges, e)
		}

		// Nodes + next frontier: deterministic iteration via sorted IDs.
		sort.Strings(newIDs)
		var nextFrontier []string
		for _, id := range newIDs {
			r, ok := newResByID[id]
			if !ok {
				continue
			}
			if _, seen := visited[id]; seen {
				continue
			}
			if excluded[id] {
				continue
			}
			if opts.MaxNodes > 0 && len(result.Nodes) >= opts.MaxNodes {
				result.TruncatedNodes++
				continue
			}
			visited[id] = depth + 1
			result.Nodes = append(result.Nodes, GraphNode{Resource: r, Depth: depth + 1})
			// Provider-managed nodes are terminal unless explicitly included.
			if !opts.IncludeManaged && r.ManagedByProvider {
				continue
			}
			nextFrontier = append(nextFrontier, id)
		}
		frontier = nextFrontier
	}

	result.Edges = pruneDanglingEdges(result.Edges, visited)

	sort.Slice(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].Depth != result.Nodes[j].Depth {
			return result.Nodes[i].Depth < result.Nodes[j].Depth
		}
		return result.Nodes[i].Resource.ID < result.Nodes[j].Resource.ID
	})
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].FromID != result.Edges[j].FromID {
			return result.Edges[i].FromID < result.Edges[j].FromID
		}
		if result.Edges[i].Kind != result.Edges[j].Kind {
			return result.Edges[i].Kind < result.Edges[j].Kind
		}
		return result.Edges[i].ToID < result.Edges[j].ToID
	})

	return result, nil
}

// pruneDanglingEdges drops edges whose endpoint was never admitted as a node.
// GraphWalk collects edges before node admission, so a node dropped purely by
// the MaxNodes cap would otherwise leave an edge referencing an absent ID.
// admitted holds exactly the nodes added to the result (visited); truncated and
// excluded nodes never appear. Mirrors GraphAll's included-set edge filter.
func pruneDanglingEdges(edges []GraphEdge, admitted map[string]int) []GraphEdge {
	kept := edges[:0]
	for _, e := range edges {
		if _, ok := admitted[e.FromID]; !ok {
			continue
		}
		if _, ok := admitted[e.ToID]; !ok {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// collectFrontierEdges walks each frontier node in the requested direction(s)
// and returns the candidate edges plus the set of unvisited endpoint IDs.
func (s *Store) collectFrontierEdges(frontier []string, dir string, kinds []string, visited map[string]int) ([]GraphEdge, map[string]struct{}, error) {
	var candidates []GraphEdge
	next := map[string]struct{}{}
	addRels := func(rels []Relationship, endpointKey func(Relationship) string) {
		for _, r := range rels {
			candidates = append(candidates, GraphEdge{FromID: r.FromID, ToID: r.ToID, Kind: r.Kind})
			if id := endpointKey(r); id != "" {
				if _, ok := visited[id]; !ok {
					next[id] = struct{}{}
				}
			}
		}
	}
	for _, id := range frontier {
		if dir == DirOut || dir == DirBoth {
			rels, err := s.RelationshipsFrom(id, kinds...)
			if err != nil {
				return nil, nil, err
			}
			addRels(rels, func(r Relationship) string { return r.ToID })
		}
		if dir == DirIn || dir == DirBoth {
			rels, err := s.RelationshipsTo(id, kinds...)
			if err != nil {
				return nil, nil, err
			}
			addRels(rels, func(r Relationship) string { return r.FromID })
		}
	}
	return candidates, next, nil
}

// classifyExcluded marks endpoints filtered out by ExcludeTypes / ExcludeRegions
// and bumps the matching counters on result. Seed is never excluded.
func classifyExcluded(newResByID map[string]Resource, seedID string, opts GraphWalkOpts, result *GraphResult) map[string]bool {
	excluded := map[string]bool{}
	for id, r := range newResByID {
		if id == seedID {
			continue
		}
		if matchTypeGlob(opts.ExcludeTypes, r.Type) {
			excluded[id] = true
			result.ExcludedTypes++
			continue
		}
		if r.Region != nil && slices.Contains(opts.ExcludeRegions, *r.Region) {
			excluded[id] = true
			result.ExcludedRegions++
		}
	}
	return excluded
}

// GraphPathOpts configures GraphPath. Direction defaults to "both" (treats the
// graph as undirected for reachability) since "can A reach B" usually wants
// the answer regardless of edge orientation.
type GraphPathOpts struct {
	MaxDepth       int      // 0 = unlimited (capped internally to 64 to bound runtime)
	Kinds          []string // empty = all
	Direction      string   // out/in/both, default both
	IncludeManaged bool
	ExcludeTypes   []string
	ExcludeRegions []string
}

// GraphPath returns the shortest edge sequence from fromID to toID using BFS
// across the relationships graph in the requested direction. Returns
// ErrNoPath if no route exists within the constraints. Nodes are returned in
// path order with Depth = distance from from. Edges are in path order.
func (s *Store) GraphPath(fromID, toID string, opts GraphPathOpts) (*GraphResult, error) {
	if fromID == toID {
		seed, err := s.GetResource(fromID)
		if err != nil {
			return nil, err
		}
		return &GraphResult{SeedID: fromID, Nodes: []GraphNode{{Resource: *seed, Depth: 0}}}, nil
	}
	from, err := s.GetResource(fromID)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetResource(toID); err != nil {
		return nil, err
	}
	dir := opts.Direction
	if dir == "" {
		dir = DirBoth
	}
	if dir != DirOut && dir != DirIn && dir != DirBoth {
		return nil, fmt.Errorf("invalid direction %q (want out|in|both)", dir)
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 || maxDepth > 64 {
		maxDepth = 64
	}

	// parent[id] = edge whose "other endpoint" (relative to direction) is the
	// predecessor on the BFS tree. Used to reconstruct the path on hit.
	parent := map[string]bfsEdge{fromID: {}}
	frontier := []string{fromID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		nextIDs, hit, err := graphPathExpandStep(s, fromID, toID, frontier, parent, dir, opts, *from)
		if err != nil {
			return nil, err
		}
		if hit != nil {
			return hit, nil
		}
		frontier = nextIDs
	}
	return nil, ErrNoPath
}

// graphPathExpandStep performs one BFS layer expansion from `frontier`,
// records parent-pointers, applies exclusion + managed-terminal filtering,
// and returns either the reconstructed path (if `toID` is hit) or the next
// layer's expandable ids.
func graphPathExpandStep(s *Store, fromID, toID string, frontier []string, parent map[string]bfsEdge, dir string, opts GraphPathOpts, fromRes Resource) ([]string, *GraphResult, error) {
	next := map[string]struct{}{}
	for _, id := range frontier {
		if err := graphPathRecordNeighbors(s, id, dir, opts.Kinds, parent, next); err != nil {
			return nil, nil, err
		}
	}
	if len(next) == 0 {
		return nil, nil, nil
	}
	ids := make([]string, 0, len(next))
	for id := range next {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	batch, err := fetchResourcesByIDs(s, ids)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]Resource, len(batch))
	for _, r := range batch {
		byID[r.ID] = r
	}
	var nextIDs []string
	for _, id := range ids {
		r, ok := byID[id]
		if !ok {
			delete(parent, id)
			continue
		}
		if id == toID {
			hit, err := reconstructPath(s, fromID, toID, parent, fromRes)
			return nil, hit, err
		}
		if matchTypeGlob(opts.ExcludeTypes, r.Type) ||
			(r.Region != nil && slices.Contains(opts.ExcludeRegions, *r.Region)) {
			delete(parent, id)
			continue
		}
		if !opts.IncludeManaged && r.ManagedByProvider {
			// Reachable but terminal — keep parent entry so a one-hop match
			// counts, but do not expand through it.
			continue
		}
		nextIDs = append(nextIDs, id)
	}
	return nextIDs, nil, nil
}

// graphPathRecordNeighbors fans `id` out in the requested direction and
// records each previously-unseen neighbor's parent-pointer into `parent`
// + adds it to `next` for the upcoming layer.
func graphPathRecordNeighbors(s *Store, id, dir string, kinds []string, parent map[string]bfsEdge, next map[string]struct{}) error {
	if dir == DirOut || dir == DirBoth {
		rels, err := s.RelationshipsFrom(id, kinds...)
		if err != nil {
			return err
		}
		for _, r := range rels {
			if _, ok := parent[r.ToID]; ok {
				continue
			}
			parent[r.ToID] = bfsEdge{parent: id, kind: r.Kind, eFrom: r.FromID, eTo: r.ToID}
			next[r.ToID] = struct{}{}
		}
	}
	if dir == DirIn || dir == DirBoth {
		rels, err := s.RelationshipsTo(id, kinds...)
		if err != nil {
			return err
		}
		for _, r := range rels {
			if _, ok := parent[r.FromID]; ok {
				continue
			}
			parent[r.FromID] = bfsEdge{parent: id, kind: r.Kind, eFrom: r.FromID, eTo: r.ToID}
			next[r.FromID] = struct{}{}
		}
	}
	return nil
}

// bfsEdge records a parent-pointer entry for GraphPath reconstruction.
// `parent` is the predecessor node on the BFS tree; eFrom/eTo preserve the
// edge's stored direction so output reflects the relationships table even
// when traversing "in".
type bfsEdge struct {
	parent     string
	kind       string
	eFrom, eTo string
}

// reconstructPath walks the BFS parent map from dest back to src and returns
// nodes + edges in forward order. Resources are loaded in one IN-query batch.
func reconstructPath(s *Store, src, dest string, parent map[string]bfsEdge, srcRes Resource) (*GraphResult, error) {
	// Walk backward.
	var ids []string
	type pe struct{ from, to, kind string }
	var pedges []pe
	cur := dest
	for cur != src {
		ids = append(ids, cur)
		p := parent[cur]
		pedges = append(pedges, pe{from: p.eFrom, to: p.eTo, kind: p.kind})
		cur = p.parent
	}
	// Reverse to forward order. ids does not include src.
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	for i, j := 0, len(pedges)-1; i < j; i, j = i+1, j-1 {
		pedges[i], pedges[j] = pedges[j], pedges[i]
	}
	batch, err := fetchResourcesByIDs(s, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Resource, len(batch))
	for _, r := range batch {
		byID[r.ID] = r
	}
	out := &GraphResult{SeedID: src}
	out.Nodes = append(out.Nodes, GraphNode{Resource: srcRes, Depth: 0})
	for i, id := range ids {
		r, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("path node %s missing from resources", id)
		}
		out.Nodes = append(out.Nodes, GraphNode{Resource: r, Depth: i + 1})
	}
	for _, e := range pedges {
		out.Edges = append(out.Edges, GraphEdge{FromID: e.from, ToID: e.to, Kind: e.kind})
	}
	return out, nil
}

// GraphAllOpts configures GraphAll. No traversal knobs (depth/kinds/direction)
// since GraphAll is store-dump style — every relationship in the DB is a
// candidate edge. Filter knobs mirror GraphWalk so the cmd layer reuses
// flag plumbing.
type GraphAllOpts struct {
	// IncludeManaged: when false (default), provider-managed resources only
	// appear if they have at least one edge to a customer-managed resource;
	// orphan managed resources (e.g. unused AWS-managed policies, built-in
	// Azure role definitions) drop out. When true, every resource is
	// included regardless of edges.
	IncludeManaged bool
	ExcludeTypes   []string
	ExcludeRegions []string
	MaxNodes       int
	MaxEdges       int
}

// GraphAll returns the whole resource graph in one shot — every customer
// resource plus every provider-managed resource that has at least one edge
// touching a customer resource. Edges are kept only when both endpoints
// survive filtering. SeedID is empty since there is no traversal seed.
//
// Prefer GraphWalk / GraphPath for seeded queries; GraphAll's working set
// scales with the full resource + relationship table.
func (s *Store) GraphAll(opts GraphAllOpts) (*GraphResult, error) {
	// Always pull managed + paginate past the default 500 cap. GraphAll
	// owns the customer-vs-managed inclusion decision below; the SQL
	// layer must hand over every row regardless.
	const pageSize = 5000
	var all []Resource
	for offset := uint64(0); ; offset += pageSize {
		page, err := s.ListResources(ResourceFilter{
			IncludeManaged: true,
			Limit:          pageSize,
			Offset:         offset,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if uint64(len(page)) < pageSize {
			break
		}
	}
	rels, err := s.ListRelationships()
	if err != nil {
		return nil, err
	}

	byID := make(map[string]Resource, len(all))
	for _, r := range all {
		byID[r.ID] = r
	}

	// connectsToCustomer[id] = true once we've seen a relationship where the
	// other endpoint is a customer (unmanaged) resource that exists in the
	// store. Self-loops on managed resources don't promote inclusion.
	connectsToCustomer := make(map[string]bool, len(byID))
	for _, rel := range rels {
		from, fromOK := byID[rel.FromID]
		to, toOK := byID[rel.ToID]
		if !fromOK || !toOK {
			continue
		}
		if !from.ManagedByProvider {
			connectsToCustomer[rel.ToID] = true
		}
		if !to.ManagedByProvider {
			connectsToCustomer[rel.FromID] = true
		}
	}

	result := &GraphResult{}
	included := make(map[string]bool, len(byID))
	for _, r := range all {
		if !opts.IncludeManaged && r.ManagedByProvider && !connectsToCustomer[r.ID] {
			continue
		}
		if matchTypeGlob(opts.ExcludeTypes, r.Type) {
			result.ExcludedTypes++
			continue
		}
		if r.Region != nil && slices.Contains(opts.ExcludeRegions, *r.Region) {
			result.ExcludedRegions++
			continue
		}
		if opts.MaxNodes > 0 && len(included) >= opts.MaxNodes {
			result.TruncatedNodes++
			continue
		}
		included[r.ID] = true
		result.Nodes = append(result.Nodes, GraphNode{Resource: r, Depth: 0})
	}

	// Stable node order for diff-friendly output. ListResources orders by
	// upsert/scan timestamp — sort by ID so two scans over the same data
	// produce identical graphs.
	sort.Slice(result.Nodes, func(i, j int) bool {
		return result.Nodes[i].Resource.ID < result.Nodes[j].Resource.ID
	})

	for _, rel := range rels {
		if !included[rel.FromID] || !included[rel.ToID] {
			continue
		}
		if opts.MaxEdges > 0 && len(result.Edges) >= opts.MaxEdges {
			result.TruncatedEdges++
			continue
		}
		result.Edges = append(result.Edges, GraphEdge{
			FromID: rel.FromID, ToID: rel.ToID, Kind: rel.Kind,
		})
	}
	return result, nil
}

// matchTypeGlob returns true if t matches any pattern. Patterns are literal
// or suffix-glob ("aws:iam:*"); a single trailing "*" is the only wildcard
// form recognised. Empty pattern list = no match.
func matchTypeGlob(patterns []string, t string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "*") {
			if strings.HasPrefix(t, p[:len(p)-1]) {
				return true
			}
			continue
		}
		if p == t {
			return true
		}
	}
	return false
}

// fetchResourcesByIDs batches an IN-clause SELECT to avoid N GetResource
// round-trips. Caller-facing ids are deterministic ResourceID hashes;
// the helpers in resources_hooks.go redirect the WHERE to root_id and
// append the current-version predicate.
func fetchResourcesByIDs(s *Store, ids []string) ([]Resource, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cols := strings.Join(resourceSelectColumns(), ", ")
	sqlText := "SELECT " + cols + " FROM resources WHERE " + resourceIDColumn() + " IN (?)" + currentVersionWhereSQL()
	q, args, err := s.sqlxIn(sqlText, ids)
	if err != nil {
		return nil, err
	}
	var out []Resource
	return out, s.selectAll(&out, q, args...)
}
