package store

import (
	"encoding/json"
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// ResourceVersion is the paid-build wire shape that carries verification
// and version-chain metadata. Embeds Resource so OSS-side field
// additions cascade automatically — adding a new field to Resource
// flows here via embedding with no parallel edit.
//
// Identity model:
//   - VersionRowID is the per-row UUIDv7 PK in the resources table.
//   - RootID is the deterministic ResourceID hash shared across every
//     row in this resource's version chain. Resource.ID (embedded)
//     also carries this hash — the resourceSelectColumns hook aliases
//     `root_id AS id` on read so OSS-shape projections stay
//     consistent.
//   - PreviousVersionID points at the immediate predecessor in the
//     chain (NULL on the root row).
//   - SupersededBy points at the successor that replaced this version
//     (NULL on the current row of every chain).
type ResourceVersion struct {
	Resource
	VerifiedAt        *string `db:"verified_at"          json:"verified_at"`
	VerifiedBy        *string `db:"verified_by"          json:"verified_by"`
	RootID            string  `db:"root_id"              json:"root_id"`
	PreviousVersionID *string `db:"previous_version_id"  json:"previous_version_id"`
	SupersededBy      *string `db:"superseded_by"        json:"superseded_by"`
	VersionRowID      string  `db:"version_row_id"       json:"version_row_id"`
}

// resourceVersionColumns lists the SELECT projection for paid reads
// targeting *ResourceVersion. Includes both `id AS version_row_id`
// and `root_id` so the chain metadata is fully populated.
func resourceVersionColumns() []string {
	return []string{
		"id AS version_row_id",
		"root_id",
		"provider",
		"account_id",
		"account_name",
		"type",
		"native_id",
		"name",
		"region",
		"zone",
		"status",
		"tags",
		"attributes",
		"managed_by_provider",
		"created_at",
		"discovered_at",
		"discovered_by",
		"verified_at",
		"verified_by",
		"previous_version_id",
		"superseded_by",
	}
}

// GetResourceVersions walks the full version chain for a resource and
// returns every row in chronological order (root first). Paid-only.
func (s *Store) GetResourceVersions(rootID string) ([]ResourceVersion, error) {
	q := sq.Select(resourceVersionColumns()...).From("resources").
		Where(sq.Eq{"root_id": rootID}).
		OrderBy("discovered_at ASC", "id ASC").
		PlaceholderFormat(s.placeholder())
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	var out []ResourceVersion
	if err := s.selectAll(&out, query, args...); err != nil {
		return nil, fmt.Errorf("list resource versions: %w", err)
	}
	return out, nil
}

// jsonEqual canonicalizes two JSON strings and reports equality. Empty
// inputs and parse failures fall back to plain string equality so the
// comparison is conservative (treats malformed JSON as not-equal to
// well-formed JSON, which is the correct change-detection behavior).
func jsonEqual(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return false
	}
	abytes, err := json.Marshal(av)
	if err != nil {
		return false
	}
	bbytes, err := json.Marshal(bv)
	if err != nil {
		return false
	}
	return string(abytes) == string(bbytes)
}

// derefStr returns the empty string for nil pointers.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
