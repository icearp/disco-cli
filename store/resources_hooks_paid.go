//go:build paid

package store

import sq "github.com/Masterminds/squirrel"

// resourceSelectColumns lists the SELECT projection for paid reads of
// the resources table when scanning into *Resource. Aliases
// `root_id AS id` so Resource.ID carries the deterministic hash (the
// stable caller-facing identifier), not the per-version UUID stored
// in the table's actual `id` column. Paid-only columns (verified_at,
// previous_version_id, etc.) are deliberately omitted — they live on
// ResourceVersion (resources_paid.go), not Resource.
//
// Merge invariant: this list must stay in lockstep with the OSS
// Resource struct's `db` tags. Adding a new OSS field to Resource
// requires appending the matching column name here.
func resourceSelectColumns() []string {
	return []string{
		"root_id AS id",
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
		// tenant_id deliberately omitted: PG-only (RLS) and no Go
		// consumer reads Resource.TenantID. Including it would break
		// SQLite paid reads where the column doesn't exist.
		//
		// workspace_id likewise omitted: it exists on both dialects
		// but RLS does the per-workspace filtering, and no Go consumer
		// reads Resource.WorkspaceID. sqlx tolerates the field staying
		// nil when the projection omits its column.
	}
}

// resourceSelectColumnsPrefixed mirrors resourceSelectColumns for
// joined queries where the table has an alias. The `root_id AS id`
// redirect is rewritten to `<prefix>.root_id AS id`.
func resourceSelectColumnsPrefixed(prefix string) []string {
	cols := []string{
		prefix + ".root_id AS id",
		prefix + ".provider",
		prefix + ".account_id",
		prefix + ".account_name",
		prefix + ".type",
		prefix + ".native_id",
		prefix + ".name",
		prefix + ".region",
		prefix + ".zone",
		prefix + ".status",
		prefix + ".tags",
		prefix + ".attributes",
		prefix + ".managed_by_provider",
		prefix + ".created_at",
		prefix + ".discovered_at",
		prefix + ".discovered_by",
	}
	return cols
}

// resourceIDColumn redirects caller-facing "id" lookups to root_id
// because the paid schema's `id` column carries a per-version UUID
// while the deterministic hash callers pass lives in `root_id`.
func resourceIDColumn() string { return "root_id" }

// applyCurrentVersionPredicate scopes a SELECT to the current row of
// each version chain. Pair with resourceSelectColumns to read the OSS-
// shape Resource projection for paid rows.
func applyCurrentVersionPredicate(q sq.SelectBuilder) sq.SelectBuilder {
	return q.Where(sq.Eq{"superseded_by": nil})
}

// currentVersionWhereSQL is the raw-SQL fragment that the paid build
// appends to hand-written WHERE clauses to scope reads to the current
// row. Always begins with " AND " so the caller's pre-existing WHERE
// clause concatenates cleanly.
func currentVersionWhereSQL() string { return " AND superseded_by IS NULL" }
