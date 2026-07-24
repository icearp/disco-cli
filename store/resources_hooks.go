package store

import sq "github.com/Masterminds/squirrel"

// resourceSelectColumns lists the SELECT projection for reads of the
// resources table into *Resource. Aliases `root_id AS id` so Resource.ID
// carries the deterministic hash (the stable caller-facing identifier), not
// the per-version UUID in the table's actual `id` column. Versioning-only
// columns (verified_at, previous_version_id, etc.) are deliberately omitted
// — they live on ResourceVersion (resources_versioning.go), not Resource.
//
// Invariant: this list must stay in lockstep with Resource's `db` tags.
// Adding a field to Resource requires appending its column name here.
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
		"reference_only",
		"created_at",
		"discovered_at",
		"discovered_by",
		// workspace_id deliberately omitted: it exists on both dialects but
		// the disco-saas RLS layer does per-workspace filtering, and no Go
		// consumer reads Resource.WorkspaceID. sqlx tolerates the field
		// staying nil when the projection omits its column.
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
		prefix + ".reference_only",
		prefix + ".created_at",
		prefix + ".discovered_at",
		prefix + ".discovered_by",
	}
	return cols
}

// resourceIDColumn redirects caller-facing "id" lookups to root_id: the
// per-row `id` column carries a per-version UUID, while the deterministic
// hash callers pass lives in `root_id`.
func resourceIDColumn() string { return "root_id" }

// applyCurrentVersionPredicate scopes a SELECT to the current row of
// each version chain. Pair with resourceSelectColumns to read the
// Resource projection for the current version of each chain.
func applyCurrentVersionPredicate(q sq.SelectBuilder) sq.SelectBuilder {
	return q.Where(sq.Eq{"superseded_by": nil})
}

// currentVersionWhereSQL is the raw-SQL fragment appended to hand-written
// WHERE clauses to scope reads to the current row. Always
// begins with " AND " so it concatenates cleanly onto the caller's existing
// WHERE clause.
func currentVersionWhereSQL() string { return " AND superseded_by IS NULL" }
