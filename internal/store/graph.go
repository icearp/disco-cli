package store

import (
	"fmt"
	"sort"
)

// Direction constants for GraphWalkOpts.
const (
	DirOut  = "out"
	DirIn   = "in"
	DirBoth = "both"
)

// GraphNode is a resource plus the BFS depth at which it was first reached.
type GraphNode struct {
	Resource Resource `json:"resource"`
	Depth    int      `json:"depth"`
}

// GraphEdge is a directed relationship between two resource IDs.
type GraphEdge struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Kind   string `json:"kind"`
}

// GraphResult is the output of GraphWalk: seed ID plus ordered nodes and edges.
type GraphResult struct {
	SeedID string      `json:"seed_id"`
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
}

// GraphWalkOpts configures GraphWalk traversal.
type GraphWalkOpts struct {
	MaxDepth  int      // inclusive; 0 = seed only
	Kinds     []string // empty = all relationship kinds
	Direction string   // "out", "in", "both" (default: "both")
	// IncludeManaged when false treats provider-managed resources as terminal:
	// they appear as edge endpoints when reached from a non-managed node, but
	// BFS does not expand through them. The seed itself is never filtered.
	IncludeManaged bool
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

	visited := map[string]int{seedID: 0}
	nodes := []GraphNode{{Resource: *seed, Depth: 0}}
	var edges []GraphEdge
	// Dedup edges so undirected peer links or double-resolved relationships
	// only appear once in the result.
	edgeKey := func(e GraphEdge) string { return e.FromID + "|" + e.Kind + "|" + e.ToID }
	seenEdge := map[string]struct{}{}

	frontier := []string{seedID}
	for depth := 0; depth < opts.MaxDepth && len(frontier) > 0; depth++ {
		next := map[string]struct{}{}
		for _, id := range frontier {
			if dir == DirOut || dir == DirBoth {
				rels, err := s.RelationshipsFrom(id, opts.Kinds...)
				if err != nil {
					return nil, err
				}
				for _, r := range rels {
					e := GraphEdge{FromID: r.FromID, ToID: r.ToID, Kind: r.Kind}
					if _, ok := seenEdge[edgeKey(e)]; !ok {
						seenEdge[edgeKey(e)] = struct{}{}
						edges = append(edges, e)
					}
					if _, ok := visited[r.ToID]; !ok {
						next[r.ToID] = struct{}{}
					}
				}
			}
			if dir == DirIn || dir == DirBoth {
				rels, err := s.RelationshipsTo(id, opts.Kinds...)
				if err != nil {
					return nil, err
				}
				for _, r := range rels {
					e := GraphEdge{FromID: r.FromID, ToID: r.ToID, Kind: r.Kind}
					if _, ok := seenEdge[edgeKey(e)]; !ok {
						seenEdge[edgeKey(e)] = struct{}{}
						edges = append(edges, e)
					}
					if _, ok := visited[r.FromID]; !ok {
						next[r.FromID] = struct{}{}
					}
				}
			}
		}
		if len(next) == 0 {
			break
		}

		// Fetch all new resources in one IN query instead of N GetResource calls.
		newIDs := make([]string, 0, len(next))
		for id := range next {
			newIDs = append(newIDs, id)
		}
		newRes, err := fetchResourcesByIDs(s, newIDs)
		if err != nil {
			return nil, err
		}
		nextFrontier := make([]string, 0, len(newRes))
		for _, r := range newRes {
			if _, ok := visited[r.ID]; ok {
				continue
			}
			visited[r.ID] = depth + 1
			nodes = append(nodes, GraphNode{Resource: r, Depth: depth + 1})
			// Provider-managed nodes are terminal unless explicitly included:
			// they show up as endpoints of the edge that reached them, but we
			// do not expand outward from them on the next BFS step.
			if !opts.IncludeManaged && r.ManagedByProvider {
				continue
			}
			nextFrontier = append(nextFrontier, r.ID)
		}

		frontier = nextFrontier
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Depth != nodes[j].Depth {
			return nodes[i].Depth < nodes[j].Depth
		}
		return nodes[i].Resource.ID < nodes[j].Resource.ID
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromID != edges[j].FromID {
			return edges[i].FromID < edges[j].FromID
		}
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}
		return edges[i].ToID < edges[j].ToID
	})

	return &GraphResult{SeedID: seedID, Nodes: nodes, Edges: edges}, nil
}

// fetchResourcesByIDs batches an IN-clause SELECT to avoid N GetResource round-trips.
func fetchResourcesByIDs(s *Store, ids []string) ([]Resource, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q, args, err := sqlxIn("SELECT * FROM resources WHERE id IN (?)", ids)
	if err != nil {
		return nil, err
	}
	var out []Resource
	return out, s.db.Select(&out, q, args...)
}
