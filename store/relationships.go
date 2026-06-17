package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// hierarchyMissingWarn is the canonical message ReportWarning fires when
// RecordHierarchy can't write the depth-1 `contains` relationship row
// because an endpoint isn't in `resources`. Closure rows still go down
// (caller intent — descendants of an unscanned parent stay meaningful).
// Operators see the warning in scan output; tests attach OnWarn to
// detect drift. Returning an error instead would force ~50 call sites
// to add `errors.Is` boilerplate for what is normally a benign skip.
const hierarchyMissingWarn = "hierarchy endpoint missing — relationship row skipped"

// Relationship represents a directed edge between two resources.
type Relationship struct {
	ID           int64   `db:"id"`
	FromID       string  `db:"from_id"`
	ToID         string  `db:"to_id"`
	Kind         string  `db:"kind"`
	Direction    string  `db:"direction"`
	Attributes   *string `db:"attributes"` // JSON
	DiscoveredAt string  `db:"discovered_at"`
	// WorkspaceID is the per-workspace RLS discriminator. Omitted from the read
	// projection (relationshipColumns) like Resource.WorkspaceID — no OSS
	// consumer reads it and the disco-saas RLS layer filters by workspace — so
	// it stays nil. (No TenantID field: disco OSS has no tenant_id column; the
	// disco-saas overlay column is simply not selected.)
	WorkspaceID *string `db:"workspace_id" json:"-"`
}

// relationshipColumns is the explicit read projection for the relationships
// table. Like scanColumns / resourceSelectColumns it omits the RLS columns
// (workspace_id, plus the tenant_id disco-saas overlays onto the same table) so
// a read ignores rather than collides with control-plane columns. Keep in sync
// with the Relationship struct's db tags. relationshipColumnsR is the same list
// qualified for an `r`-aliased relationships table in a join.
const (
	relationshipColumns  = "id, from_id, to_id, kind, direction, attributes, discovered_at"
	relationshipColumnsR = "r.id, r.from_id, r.to_id, r.kind, r.direction, r.attributes, r.discovered_at"
)

// Relationship kind constants.
const (
	RelContains          = "contains"
	RelAttachedTo        = "attached-to"
	RelUses              = "uses"
	RelRoutesTo          = "routes-to"
	RelPeer              = "peer"
	RelAssumes           = "assumes"
	RelCrossAccountTrust = "cross-account-trust" // AWS IAM role trust → foreign account/role/user
	RelCrossSubRBAC      = "cross-sub-rbac"      // Azure RBAC assignment → resource in different sub
	RelCrossProjectIAM   = "cross-project-iam"   // GCP IAM binding → SA in different project
	RelBoundedBy         = "bounded-by"          // IAM principal → permission-boundary policy (AWS) or analogue
)

// RelEdge is one relationship to upsert in a batch via UpsertRelationships.
// Empty Direction defaults to "directed". Mirrors UpsertRelationship's args.
type RelEdge struct {
	FromID    string
	ToID      string
	Kind      string
	Direction string
	Attrs     *string
}

// relBuffer accumulates edges for a buffered store (see Store.BeginRelBuffer).
// The mutex guards against a single resolver fanning out concurrent
// UpsertRelationship calls onto its buffered store.
type relBuffer struct {
	mu    sync.Mutex
	edges []RelEdge
}

const upsertRelationshipSQL = `
	INSERT INTO relationships (from_id, to_id, kind, direction, attributes, discovered_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(from_id, to_id, kind) DO UPDATE SET
		attributes    = excluded.attributes,
		discovered_at = excluded.discovered_at`

// UpsertRelationship inserts a relationship, ignoring conflicts (idempotent).
// On a buffered store (BeginRelBuffer) the edge is accumulated and written later
// by FlushRelBuffer in a single transaction instead of its own autocommit.
func (s *Store) UpsertRelationship(fromID, toID, kind, direction string, attrs *string) error {
	if direction == "" {
		direction = "directed"
	}
	if s.relBuf != nil {
		s.relBuf.mu.Lock()
		s.relBuf.edges = append(s.relBuf.edges, RelEdge{fromID, toID, kind, direction, attrs})
		s.relBuf.mu.Unlock()
		if s.activeCounter != nil {
			s.activeCounter.Add(1)
		}
		return nil
	}
	discoveredAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.exec(upsertRelationshipSQL, fromID, toID, kind, direction, attrs, discoveredAt)
	if err != nil {
		return fmt.Errorf("upsert relationship %s -[%s]-> %s: %w", fromID, kind, toID, err)
	}
	if s.activeCounter != nil {
		s.activeCounter.Add(1)
	}
	return nil
}

// UpsertRelationships upserts many relationships in a single transaction with a
// reused prepared statement — the batch form of UpsertRelationship. Idempotent
// (same ON CONFLICT). Empty input is a no-op. Callers that buffer via
// BeginRelBuffer reach this through FlushRelBuffer; it is also callable directly.
func (s *Store) UpsertRelationships(edges []RelEdge) error {
	if len(edges) == 0 {
		return nil
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Preparex(tx.Rebind(upsertRelationshipSQL))
	if err != nil {
		return fmt.Errorf("prepare upsert relationships: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	discoveredAt := time.Now().UTC().Format(time.RFC3339)
	for _, e := range edges {
		dir := e.Direction
		if dir == "" {
			dir = "directed"
		}
		if _, err := stmt.Exec(e.FromID, e.ToID, e.Kind, dir, e.Attrs, discoveredAt); err != nil {
			return fmt.Errorf("upsert relationship %s -[%s]-> %s: %w", e.FromID, e.Kind, e.ToID, err)
		}
	}
	return tx.Commit()
}

// ReversedContainsEdges returns `contains` rows where `to_id` is an
// ancestor of `from_id` per hierarchy_closure — i.e. the edge points
// child→parent instead of the canonical parent→child. Always-empty result
// is the invariant; non-empty means a scanner regressed and is emitting
// reversed direction. Used by the store-side test that guards future
// drift; CI fails on any non-empty return.
func (s *Store) ReversedContainsEdges() ([]Relationship, error) {
	const q = `
		SELECT ` + relationshipColumnsR + `
		  FROM relationships r
		  JOIN hierarchy_closure hc
		    ON hc.descendant_id = r.from_id
		   AND hc.ancestor_id   = r.to_id
		   AND hc.depth >= 1
		 WHERE r.kind = 'contains'`
	var rs []Relationship
	if err := s.selectAll(&rs, q); err != nil {
		return nil, fmt.Errorf("reversed contains edges: %w", err)
	}
	return rs, nil
}

// ListRelationships returns every edge in the store. Used by GraphAll for
// the unfiltered "complete graph" subcommand; per-seed walks should prefer
// RelationshipsFrom / RelationshipsTo so they don't pull the whole table.
func (s *Store) ListRelationships() ([]Relationship, error) {
	var rs []Relationship
	if err := s.selectAll(&rs, "SELECT "+relationshipColumns+" FROM relationships ORDER BY from_id, kind, to_id"); err != nil {
		return nil, fmt.Errorf("list relationships: %w", err)
	}
	return rs, nil
}

// RelationshipsFrom returns all edges originating from a resource.
func (s *Store) RelationshipsFrom(fromID string, kinds ...string) ([]Relationship, error) {
	if len(kinds) == 0 {
		var rels []Relationship
		return rels, s.selectAll(&rels,
			"SELECT "+relationshipColumns+" FROM relationships WHERE from_id = ? ORDER BY kind", fromID)
	}
	query, args, err := s.sqlxIn(
		"SELECT "+relationshipColumns+" FROM relationships WHERE from_id = ? AND kind IN (?) ORDER BY kind",
		fromID, kinds,
	)
	if err != nil {
		return nil, err
	}
	var rels []Relationship
	return rels, s.selectAll(&rels, query, args...)
}

// RelationshipsTo returns all edges pointing to a resource.
func (s *Store) RelationshipsTo(toID string, kinds ...string) ([]Relationship, error) {
	if len(kinds) == 0 {
		var rels []Relationship
		return rels, s.selectAll(&rels,
			"SELECT "+relationshipColumns+" FROM relationships WHERE to_id = ? ORDER BY kind", toID)
	}
	query, args, err := s.sqlxIn(
		"SELECT "+relationshipColumns+" FROM relationships WHERE to_id = ? AND kind IN (?) ORDER BY kind",
		toID, kinds,
	)
	if err != nil {
		return nil, err
	}
	var rels []Relationship
	return rels, s.selectAll(&rels, query, args...)
}

// RecordHierarchy writes both halves of a parent/child relationship in a
// single transaction:
//
//   - the child's self-entry in hierarchy_closure (depth 0) plus one
//     closure row per ancestor of parent extending depth + 1, so the
//     transitive closure stays O(1) for "all descendants of X" queries
//     (org→folder→project shape produces nine rows: each node's self
//     entry plus org→folder, org→project, folder→project);
//   - a depth-1 `parent → child contains` row in `relationships` so
//     GraphWalk (which reads only `relationships`) sees the hierarchy
//     edge. This unification is what makes Azure/GCP hierarchy visible
//     in `disco graph` — those providers record hierarchy via closure
//     only.
//
// Missing endpoints (a pair referring to a resource not in `resources`,
// e.g. an Azure RG not scanned in this pass) skip the relationship row
// and emit a ScanWarning via ReportWarning — operators see the drift,
// callers stay simple. Closure rows always go down regardless.
func (s *Store) RecordHierarchy(childID, parentID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	missing, err := s.recordHierarchyTx(tx, childID, parentID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if missing {
		s.ReportWarning(ScanWarning{
			Provider: "store", Service: "hierarchy",
			Scope: childID + "→" + parentID, Message: hierarchyMissingWarn,
		})
	}
	return nil
}

// recordHierarchyTx is the per-pair body shared by RecordHierarchy and
// RecordHierarchyBatch. Closure rows go down unconditionally; the
// relationship row is gated on both endpoints existing in `resources`.
// Returns (missing=true, nil) when the gate fails — caller fires a
// ScanWarning so operators see the drift without each scanner needing
// `errors.Is` boilerplate. Real DB errors propagate normally.
func (s *Store) recordHierarchyTx(tx *sql.Tx, childID, parentID string) (missing bool, err error) {
	if _, err := tx.Exec(s.rebind(`
		INSERT INTO hierarchy_closure (ancestor_id, descendant_id, depth)
		VALUES (?, ?, 0)
		ON CONFLICT (ancestor_id, descendant_id) DO NOTHING`), childID, childID); err != nil {
		return false, fmt.Errorf("closure self-entry %s: %w", childID, err)
	}
	if _, err := tx.Exec(s.rebind(`
		INSERT INTO hierarchy_closure (ancestor_id, descendant_id, depth)
		SELECT hc.ancestor_id, ?, hc.depth + 1
		FROM hierarchy_closure hc
		WHERE hc.descendant_id = ?
		ON CONFLICT (ancestor_id, descendant_id) DO NOTHING`), childID, parentID); err != nil {
		return false, fmt.Errorf("closure ancestor entries %s: %w", childID, err)
	}
	if childID == parentID {
		return false, nil
	}
	parentExists, err := s.resourceExistsTx(tx, parentID)
	if err != nil {
		return false, fmt.Errorf("check parent %s: %w", parentID, err)
	}
	childExists, err := s.resourceExistsTx(tx, childID)
	if err != nil {
		return false, fmt.Errorf("check child %s: %w", childID, err)
	}
	if !parentExists || !childExists {
		return true, nil
	}
	if _, err := tx.Exec(s.rebind(`
		INSERT INTO relationships (from_id, to_id, kind, direction, discovered_at)
		VALUES (?, ?, 'contains', 'directed', ?)
		ON CONFLICT (from_id, to_id, kind) DO NOTHING`),
		parentID, childID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return false, fmt.Errorf("hierarchy relationship row %s→%s: %w", parentID, childID, err)
	}
	return false, nil
}

func (s *Store) resourceExistsTx(tx *sql.Tx, id string) (bool, error) {
	// Caller-facing id is the deterministic ResourceID hash. Under
	// paid that's resources.root_id with a current-version filter;
	// under OSS it's resources.id directly. Hooks resolve the
	// difference.
	sqlText := "SELECT 1 FROM resources WHERE " + resourceIDColumn() + " = ?" + currentVersionWhereSQL() + " LIMIT 1"
	var n int
	if err := tx.QueryRow(s.rebind(sqlText), id).Scan(&n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecordHierarchyBatch is the multi-pair form of RecordHierarchy. Pairs
// are `[2]string{childID, parentID}`. Missing-endpoint pairs collect
// into a single ScanWarning at the end so one warning per call surfaces
// the drift without spamming on busy scans.
func (s *Store) RecordHierarchyBatch(pairs [][2]string) error {
	if len(pairs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var missingCount int
	var firstMissingScope string
	for _, p := range pairs {
		missing, err := s.recordHierarchyTx(tx, p[0], p[1])
		if err != nil {
			return err
		}
		if missing {
			if missingCount == 0 {
				firstMissingScope = p[0] + "→" + p[1]
			}
			missingCount++
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if missingCount > 0 {
		s.ReportWarning(ScanWarning{
			Provider: "store", Service: "hierarchy",
			Scope:   firstMissingScope,
			Message: fmt.Sprintf("%s (and %d more)", hierarchyMissingWarn, missingCount-1),
		})
	}
	return nil
}

// sqlxIn expands slice args for IN clauses using sqlx.In and rebinds to the
// active driver's placeholder format (Question for SQLite, Dollar for
// Postgres). Method on *Store so dialect awareness flows through.
func (s *Store) sqlxIn(query string, args ...any) (string, []any, error) {
	q, a, err := sqlx.In(query, args...)
	if err != nil {
		return "", nil, err
	}
	return s.rebind(q), a, nil
}
