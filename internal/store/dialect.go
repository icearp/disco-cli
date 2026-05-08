package store

import (
	"database/sql"

	sq "github.com/Masterminds/squirrel"
)

// placeholder returns the squirrel placeholder format matching the active
// driver. SQLite uses `?`; Postgres uses `$N`.
func (s *Store) placeholder() sq.PlaceholderFormat {
	if s.driver == driverPostgres {
		return sq.Dollar
	}
	return sq.Question
}

// rebind translates a `?`-placeholder query to the driver's native format.
// For SQLite this is a no-op; for Postgres it rewrites to `$1, $2, ...`.
// Always pass through s.db.Rebind so future driver additions stay opaque.
func (s *Store) rebind(q string) string {
	return s.db.Rebind(q)
}

// exec proxies db.Exec with auto-rebind of `?` placeholders.
func (s *Store) exec(q string, args ...any) (sql.Result, error) {
	return s.db.Exec(s.db.Rebind(q), args...)
}

// query proxies db.Query with auto-rebind.
func (s *Store) query(q string, args ...any) (*sql.Rows, error) {
	return s.db.Query(s.db.Rebind(q), args...)
}

// queryRow proxies db.QueryRow with auto-rebind.
func (s *Store) queryRow(q string, args ...any) *sql.Row {
	return s.db.QueryRow(s.db.Rebind(q), args...)
}

// get proxies db.Get with auto-rebind.
func (s *Store) get(dest any, q string, args ...any) error {
	return s.db.Get(dest, s.db.Rebind(q), args...)
}

// selectAll proxies db.Select with auto-rebind. (Named selectAll because
// `select` is a Go keyword.)
func (s *Store) selectAll(dest any, q string, args ...any) error {
	return s.db.Select(dest, s.db.Rebind(q), args...)
}

// tagJSONValueExists returns an EXISTS-clause SQL fragment that matches if
// any tag value equals the bound parameter. SQLite uses json_each(tags);
// Postgres uses jsonb_each_text(tags). Caller binds one string arg.
func (s *Store) tagJSONValueExists() string {
	if s.driver == driverPostgres {
		return "EXISTS (SELECT 1 FROM jsonb_each_text(tags) WHERE value = ?)"
	}
	return "EXISTS (SELECT 1 FROM json_each(tags) WHERE json_each.value = ?)"
}

// tagJSONFilter returns an SQL fragment that extracts the named JSON tag key
// from the `tags` column and a placeholder argument for the value. Branches
// json_extract (SQLite) vs `->>` (Postgres). Caller appends the value compare
// (`= ?` or ` IS NOT NULL`) and supplies the value arg if needed.
//
// SQLite shape: `json_extract(tags, ?)` with arg `"$.k"`.
// Postgres shape: `tags ->> ?` with arg `"k"`.
func (s *Store) tagJSONFilter(key string) (frag, arg string) {
	if s.driver == driverPostgres {
		return "tags ->> ?", key
	}
	return "json_extract(tags, ?)", "$." + key
}
