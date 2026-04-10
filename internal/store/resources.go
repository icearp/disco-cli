package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
)

// Resource represents a discovered cloud resource.
type Resource struct {
	ID             string  `db:"id"`
	Provider       string  `db:"provider"`
	AccountID      string  `db:"account_id"`
	AccountName    *string `db:"account_name"`
	Type           string  `db:"type"`
	NativeID       string  `db:"native_id"`
	Name           *string `db:"name"`
	Region         *string `db:"region"`
	Zone           *string `db:"zone"`
	Status         *string `db:"status"`
	TagsJSON       *string `db:"tags"`
	AttributesJSON string  `db:"attributes"` // JSON blob
	CreatedAt      *string `db:"created_at"`
	DiscoveredAt   string  `db:"discovered_at"`
	DiscoveredBy   string  `db:"discovered_by"`
	VerifiedAt     *string `db:"verified_at"` // updated each time the resource is seen in a scan
	VerifiedBy     *string `db:"verified_by"` // scan ID that last verified this resource
}

// ResourceID computes a stable deterministic ID for a resource.
func ResourceID(provider, accountID, resourceType, nativeID string) string {
	h := sha256.Sum256([]byte(provider + "|" + accountID + "|" + resourceType + "|" + nativeID))
	return fmt.Sprintf("%x", h[:16])
}

// UpsertResource inserts or replaces a single resource. Delegates to UpsertResources.
// Returns the number of newly inserted resources (0 or 1).
func (s *Store) UpsertResource(r *Resource) (int, error) {
	return s.UpsertResources([]*Resource{r})
}

// UpsertResources bulk-upserts resources in a single transaction.
// Returns the number of resources that were newly inserted (not previously in the DB).
func (s *Store) UpsertResources(resources []*Resource) (inserted int, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Assign IDs before the pre-query so we know which keys to check.
	for _, r := range resources {
		if r.ID == "" {
			r.ID = ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
		}
		if r.DiscoveredAt == "" {
			r.DiscoveredAt = now
		}
		r.VerifiedAt = &now
		r.VerifiedBy = &r.DiscoveredBy
	}

	// Count how many of these IDs already exist in the DB.
	// inserted = len(resources) - existing.
	ids := make([]string, len(resources))
	for i, r := range resources {
		ids[i] = r.ID
	}
	q, args, _ := sq.Select("COUNT(*)").From("resources").
		Where(sq.Eq{"id": ids}).PlaceholderFormat(sq.Question).ToSql()
	var existing int
	if err := s.db.Get(&existing, q, args...); err != nil {
		return 0, fmt.Errorf("count existing resources: %w", err)
	}
	inserted = len(resources) - existing

	tx, err := s.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, r := range resources {
		if _, err := tx.NamedExec(`
			INSERT INTO resources
				(id, provider, account_id, account_name, type, native_id, name,
				 region, zone, status, tags, attributes, created_at, discovered_at, discovered_by, verified_at, verified_by)
			VALUES
				(:id, :provider, :account_id, :account_name, :type, :native_id, :name,
				 :region, :zone, :status, :tags, :attributes, :created_at, :discovered_at, :discovered_by, :verified_at, :verified_by)
			ON CONFLICT(id) DO UPDATE SET
				name        = excluded.name,
				status      = excluded.status,
				tags        = excluded.tags,
				attributes  = excluded.attributes,
				verified_at = excluded.verified_at,
				verified_by = excluded.verified_by`, r); err != nil {
			return 0, fmt.Errorf("upsert resource %s: %w", r.ID, err)
		}
	}
	return inserted, tx.Commit()
}

// ResourceFilter defines optional filters for ListResources.
type ResourceFilter struct {
	Provider     string
	AccountID    string
	Types        []string
	Regions      []string
	Status       string
	DiscoveredBy string
	TagKey       string
	TagValue     string
	Limit        uint64
	Offset       uint64
}

// ListResources returns resources matching the given filters.
func (s *Store) ListResources(f ResourceFilter) ([]Resource, error) {
	q := sq.Select("*").From("resources")

	if f.Provider != "" {
		q = q.Where(sq.Eq{"provider": f.Provider})
	}
	if f.AccountID != "" {
		q = q.Where(sq.Eq{"account_id": f.AccountID})
	}
	if len(f.Types) > 0 {
		q = q.Where(sq.Eq{"type": f.Types})
	}
	if len(f.Regions) > 0 {
		q = q.Where(sq.Eq{"region": f.Regions})
	}
	if f.Status != "" {
		q = q.Where(sq.Eq{"status": f.Status})
	}
	if f.DiscoveredBy != "" {
		q = q.Where(sq.Eq{"discovered_by": f.DiscoveredBy})
	}
	if f.TagKey != "" && f.TagValue != "" {
		q = q.Where("json_extract(tags, ?) = ?", "$."+f.TagKey, f.TagValue)
	} else if f.TagKey != "" {
		q = q.Where("json_extract(tags, ?) IS NOT NULL", "$."+f.TagKey)
	}

	limit := f.Limit
	if limit == 0 {
		limit = 500
	}
	q = q.Limit(limit).Offset(f.Offset).OrderBy("provider", "type", "name")

	query, args, err := q.PlaceholderFormat(sq.Question).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	var results []Resource
	if err := s.db.Select(&results, query, args...); err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	return results, nil
}

// GetResource retrieves a single resource by ID.
func (s *Store) GetResource(id string) (*Resource, error) {
	var r Resource
	if err := s.db.Get(&r, "SELECT * FROM resources WHERE id = ?", id); err != nil {
		return nil, fmt.Errorf("get resource %s: %w", id, err)
	}
	return &r, nil
}

// UnmarshalAttributes unmarshals the resource's JSON attributes into v.
func (r *Resource) UnmarshalAttributes(v any) error {
	return json.Unmarshal([]byte(r.AttributesJSON), v)
}

// CountResourcesByScan returns the number of resources recorded under a scan ID.
func (s *Store) CountResourcesByScan(scanID string) (int, error) {
	var n int
	err := s.db.Get(&n, "SELECT COUNT(*) FROM resources WHERE discovered_by = ?", scanID)
	return n, err
}

// Tags unmarshals the resource's JSON tags into a string map.
func (r *Resource) Tags() (map[string]string, error) {
	if r.TagsJSON == nil {
		return nil, nil
	}
	var tags map[string]string
	return tags, json.Unmarshal([]byte(*r.TagsJSON), &tags)
}

// DescendantsOf returns all resources that are descendants of parentID (any depth).
func (s *Store) DescendantsOf(parentID string, f ResourceFilter) ([]Resource, error) {
	q := sq.Select("r.*").
		From("resources r").
		Join("hierarchy_closure hc ON hc.descendant_id = r.id").
		Where(sq.Eq{"hc.ancestor_id": parentID}).
		Where(sq.Gt{"hc.depth": 0})

	if f.Provider != "" {
		q = q.Where(sq.Eq{"r.provider": f.Provider})
	}
	if len(f.Types) > 0 {
		q = q.Where(sq.Eq{"r.type": f.Types})
	}
	if f.Status != "" {
		q = q.Where(sq.Eq{"r.status": f.Status})
	}

	query, args, err := q.PlaceholderFormat(sq.Question).ToSql()
	if err != nil {
		return nil, err
	}
	var results []Resource
	return results, s.db.Select(&results, query, args...)
}
