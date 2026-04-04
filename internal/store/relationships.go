package store

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// Relationship represents a directed edge between two resources.
type Relationship struct {
	ID           int64   `db:"id"`
	FromID       string  `db:"from_id"`
	ToID         string  `db:"to_id"`
	Kind         string  `db:"kind"`
	Direction    string  `db:"direction"`
	Attributes   *string `db:"attributes"` // JSON
	DiscoveredAt string  `db:"discovered_at"`
}

// Relationship kind constants.
const (
	RelContains   = "contains"
	RelAttachedTo = "attached-to"
	RelUses       = "uses"
	RelRoutesTo   = "routes-to"
	RelPeer       = "peer"
	RelAssumes    = "assumes"
)

// UpsertRelationship inserts a relationship, ignoring conflicts (idempotent).
func (s *Store) UpsertRelationship(fromID, toID, kind, direction string, attrs *string) error {
	if direction == "" {
		direction = "directed"
	}
	discoveredAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO relationships (from_id, to_id, kind, direction, attributes, discovered_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(from_id, to_id, kind) DO UPDATE SET
			attributes    = excluded.attributes,
			discovered_at = excluded.discovered_at`,
		fromID, toID, kind, direction, attrs, discoveredAt,
	)
	if err != nil {
		return fmt.Errorf("upsert relationship %s -[%s]-> %s: %w", fromID, kind, toID, err)
	}
	return nil
}

// RelationshipsFrom returns all edges originating from a resource.
func (s *Store) RelationshipsFrom(fromID string, kinds ...string) ([]Relationship, error) {
	if len(kinds) == 0 {
		var rels []Relationship
		return rels, s.db.Select(&rels,
			"SELECT * FROM relationships WHERE from_id = ? ORDER BY kind", fromID)
	}
	query, args, err := sqlxIn(
		"SELECT * FROM relationships WHERE from_id = ? AND kind IN (?) ORDER BY kind",
		fromID, kinds,
	)
	if err != nil {
		return nil, err
	}
	var rels []Relationship
	return rels, s.db.Select(&rels, query, args...)
}

// RelationshipsTo returns all edges pointing to a resource.
func (s *Store) RelationshipsTo(toID string, kinds ...string) ([]Relationship, error) {
	if len(kinds) == 0 {
		var rels []Relationship
		return rels, s.db.Select(&rels,
			"SELECT * FROM relationships WHERE to_id = ? ORDER BY kind", toID)
	}
	query, args, err := sqlxIn(
		"SELECT * FROM relationships WHERE to_id = ? AND kind IN (?) ORDER BY kind",
		toID, kinds,
	)
	if err != nil {
		return nil, err
	}
	var rels []Relationship
	return rels, s.db.Select(&rels, query, args...)
}

// NeighboursOf returns all resources directly connected to id (in either direction).
func (s *Store) NeighboursOf(id string) ([]Resource, error) {
	var results []Resource
	err := s.db.Select(&results, `
		SELECT r.* FROM resources r
		JOIN relationships rel ON rel.to_id = r.id
		WHERE rel.from_id = ?
		UNION
		SELECT r.* FROM resources r
		JOIN relationships rel ON rel.from_id = r.id
		WHERE rel.to_id = ?`, id, id)
	return results, err
}

// AddToHierarchyClosure inserts closure table entries for a new child resource.
// Must be called after upserting any resource that has a parent_id set.
//
// A closure table stores every ancestor→descendant pair at every depth, enabling
// O(1) "all descendants of X" queries via a single JOIN instead of a recursive CTE.
// For example, given org→folder→project, the table holds:
//
//	(org, org, 0), (org, folder, 1), (org, project, 2)
//	(folder, folder, 0), (folder, project, 1)
//	(project, project, 0)
//
// The INSERT...SELECT derives new rows by walking all existing ancestors of parentID
// and extending their depth by 1 to reach childID.
func (s *Store) AddToHierarchyClosure(childID, parentID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Every node is its own ancestor at depth 0.
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO hierarchy_closure (ancestor_id, descendant_id, depth)
		VALUES (?, ?, 0)`, childID, childID); err != nil {
		return fmt.Errorf("closure self-entry: %w", err)
	}

	// For every ancestor of parentID (including itself), insert ancestor→child at depth+1.
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO hierarchy_closure (ancestor_id, descendant_id, depth)
		SELECT hc.ancestor_id, ?, hc.depth + 1
		FROM hierarchy_closure hc
		WHERE hc.descendant_id = ?`, childID, parentID); err != nil {
		return fmt.Errorf("closure ancestor entries: %w", err)
	}

	return tx.Commit()
}

// BatchAddToHierarchyClosure inserts closure entries for multiple child→parent
// pairs in a single transaction. Prefer this over repeated AddToHierarchyClosure
// calls when processing a batch of resources.
func (s *Store) BatchAddToHierarchyClosure(pairs [][2]string) error {
	if len(pairs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range pairs {
		childID, parentID := p[0], p[1]
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO hierarchy_closure (ancestor_id, descendant_id, depth)
			VALUES (?, ?, 0)`, childID, childID); err != nil {
			return fmt.Errorf("closure self-entry %s: %w", childID, err)
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO hierarchy_closure (ancestor_id, descendant_id, depth)
			SELECT hc.ancestor_id, ?, hc.depth + 1
			FROM hierarchy_closure hc
			WHERE hc.descendant_id = ?`, childID, parentID); err != nil {
			return fmt.Errorf("closure ancestor entries %s: %w", childID, err)
		}
	}
	return tx.Commit()
}

// sqlxIn expands slice args for IN clauses using sqlx.In and rebinds for SQLite.
func sqlxIn(query string, args ...any) (string, []any, error) {
	q, a, err := sqlx.In(query, args...)
	if err != nil {
		return "", nil, err
	}
	return sqlx.Rebind(sqlx.QUESTION, q), a, nil
}
