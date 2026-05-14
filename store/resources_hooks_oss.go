//go:build !paid

package store

import sq "github.com/Masterminds/squirrel"

// resourceSelectColumns lists the SELECT projection for OSS reads of
// the resources table. Returns `*` so existing queries keep their
// SELECT-* behavior — the OSS schema has exactly the columns the
// Resource struct expects.
func resourceSelectColumns() []string { return []string{"*"} }

// resourceSelectColumnsPrefixed returns the projection used in joined
// queries where the resources table has an alias (e.g. `r`). Returns
// `prefix + ".*"` in OSS (since SELECT * is safe — schema has no
// extra columns).
func resourceSelectColumnsPrefixed(prefix string) []string {
	return []string{prefix + ".*"}
}

// resourceIDColumn returns the column name that holds Resource.ID's
// value. In OSS that's the table PK `id`. The paid build redirects
// to `root_id` because its `id` column carries a per-version UUID.
func resourceIDColumn() string { return "id" }

// applyCurrentVersionPredicate is a no-op in OSS — there is no
// version chain to filter, every row is "current". Paid adds the
// `superseded_by IS NULL` clause that scopes reads to current rows.
func applyCurrentVersionPredicate(q sq.SelectBuilder) sq.SelectBuilder {
	return q
}

// currentVersionWhereSQL is the raw-SQL companion to
// applyCurrentVersionPredicate. Returns the SQL fragment to append to
// hand-written WHERE clauses (raw-SQL reads in graph.go etc.).
// OSS returns empty; paid returns " AND superseded_by IS NULL".
func currentVersionWhereSQL() string { return "" }
