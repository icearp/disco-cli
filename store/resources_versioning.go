package store

import (
	"encoding/json"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
)

// ResourceVersion is the paid-build wire shape that carries verification
// and version-chain metadata. Embeds Resource, so new Resource fields
// cascade here automatically — no parallel edit needed.
//
// Identity model:
//   - VersionRowID is the per-row UUIDv7 PK in the resources table.
//   - RootID is the deterministic ResourceID hash shared across every row in
//     this resource's version chain. Resource.ID (embedded) also carries
//     this hash — the resourceSelectColumns hook aliases `root_id AS id` on
//     read so OSS-shape projections stay consistent.
//   - PreviousVersionID points at the immediate predecessor in the chain
//     (NULL on the root row).
//   - SupersededBy points at the successor that replaced this version (NULL
//     on the current row of every chain).
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
// returns every row in chronological order (root first). Surfaced in OSS via
// `disco history`.
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

// jsonEqual canonicalizes two JSON strings and reports equality. Empty inputs
// and parse failures fall back to plain string equality, so malformed JSON
// never matches well-formed JSON — the correct change-detection behavior.
//
// Both sides run through canonicalizeJSONValue before re-marshalling, which
// recursively canonicalizes any string leaf that is itself a JSON
// object/array. This absorbs non-deterministic key ordering inside embedded
// policy documents — AWS returns the KMS key Policy (and S3/SNS/SQS resource
// policies, IAM assume-role docs) as an opaque JSON *string* with
// Condition-map keys in random order, which would otherwise version-split an
// unchanged resource every scan. A genuinely different policy still produces
// different canonical bytes, so real changes are still detected.
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
	abytes, err := json.Marshal(canonicalizeJSONValue(av))
	if err != nil {
		return false
	}
	bbytes, err := json.Marshal(canonicalizeJSONValue(bv))
	if err != nil {
		return false
	}
	return string(abytes) == string(bbytes)
}

// canonicalizeJSONValue recursively normalizes a decoded JSON value so
// json.Marshal (which sorts object keys) yields order-stable bytes at every
// nesting level — including inside string leaves that themselves carry an
// embedded JSON object/array. A string leaf is reinterpreted as JSON only
// when it begins with '{' or '[' AND parses cleanly; otherwise it's left
// untouched (so values like "123" or "true" stay literal strings, and
// malformed embedded JSON passes through unchanged).
func canonicalizeJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = canonicalizeJSONValue(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = canonicalizeJSONValue(val)
		}
		return t
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
			return t
		}
		var inner any
		if err := json.Unmarshal([]byte(t), &inner); err != nil {
			return t
		}
		canon, err := json.Marshal(canonicalizeJSONValue(inner))
		if err != nil {
			return t
		}
		return string(canon)
	default:
		return v
	}
}

// derefStr returns the empty string for nil pointers.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
