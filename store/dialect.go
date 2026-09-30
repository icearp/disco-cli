package store

import (
	"database/sql"

	sq "github.com/Masterminds/squirrel"
)

// sqlxExt is the read+write surface satisfied by both *sqlx.DB and *sqlx.Tx.
// Used by the dialect helpers below so the same Store value can run queries
// against either a pool or a caller-owned transaction (see WrapTx).
type sqlxExt interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Get(dest any, query string, args ...any) error
	Select(dest any, query string, args ...any) error
	Rebind(query string) string
}

// ext returns the active query target. For Stores produced by Open / OpenPostgres
// it is the underlying *sqlx.DB; for Stores produced by WrapTx it is the
// caller-owned *sqlx.Tx.
func (s *Store) ext() sqlxExt {
	if s.tx != nil {
		return s.tx
	}
	return s.db
}

// placeholder returns the squirrel placeholder format matching the active
// driver. SQLite uses `?`; Postgres uses `$N`.
// newestFirst is the ORDER BY for scans and check_runs, newest first.
// started_at has 1s resolution (nowExpr), so a same-second pair needs a
// tiebreak. SQLite's rowid is insertion order, so `latest` stays the row
// created last; Postgres has no rowid, and its fallback, the random hex id, is
// deterministic but not creation order. Ids stay random because users paste
// 8-char prefixes, which a time-ordered id (UUIDv7) would make collide.
func (s *Store) newestFirst() string {
	if s.driver == driverPostgres {
		return "started_at DESC, id DESC"
	}
	return "started_at DESC, rowid DESC"
}

func (s *Store) placeholder() sq.PlaceholderFormat {
	if s.driver == driverPostgres {
		return sq.Dollar
	}
	return sq.Question
}

// rebind translates a `?`-placeholder query to the driver's native format.
// For SQLite this is a no-op; for Postgres it rewrites to `$1, $2, ...`.
func (s *Store) rebind(q string) string {
	return s.ext().Rebind(q)
}

// exec proxies Exec with auto-rebind of `?` placeholders.
func (s *Store) exec(q string, args ...any) (sql.Result, error) {
	e := s.ext()
	return e.Exec(e.Rebind(q), args...)
}

// queryRow proxies QueryRow with auto-rebind.
func (s *Store) queryRow(q string, args ...any) *sql.Row {
	e := s.ext()
	return e.QueryRow(e.Rebind(q), args...)
}

// get proxies Get with auto-rebind.
func (s *Store) get(dest any, q string, args ...any) error {
	e := s.ext()
	return e.Get(dest, e.Rebind(q), args...)
}

// selectAll proxies Select with auto-rebind. (Named selectAll because
// `select` is a Go keyword.)
func (s *Store) selectAll(dest any, q string, args ...any) error {
	e := s.ext()
	return e.Select(dest, e.Rebind(q), args...)
}

// nowExpr returns a driver-appropriate SQL expression for the current UTC
// time as RFC3339 ("2006-01-02T15:04:05Z"). Both branches must render the
// same bytes: the columns they feed are compared and ordered as TEXT, so a
// dialect that formatted differently would sort its own rows apart. Embed
// directly in INSERT/UPDATE statements.
//
// The trailing "Z" is load-bearing rather than decoration. These columns are
// TEXT and their readers compare and order them AS TEXT, so lexicographic
// order is chronological only while every value is fixed-width UTC. The
// zoneless form below carries a space at byte 10, which sorts under every
// canonical value of its own date or later. A reader that casts instead has a
// second reason to want the offset -- without one the cast resolves against
// the session TimeZone rather than UTC -- but casting a column it does not
// write is a liability of its own: one value that will not cast raises
// 22007/22008 and takes the whole statement down.
//
// This emitted a zoneless "YYYY-MM-DD HH:MM:SS" until v0.31.0, which the
// schema's own column comments had always described as RFC3339. Rows written
// before that keep the old shape -- no migration rewrites them, see
// migrations/pg/016 -- so a store can hold both and readers accept both; see
// ToRFC3339.
func (s *Store) nowExpr() string {
	if s.driver == driverPostgres {
		// to_char emits a literal character only when it is double-quoted,
		// hence "T" and "Z" rather than bare T and Z.
		return `to_char(now() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	}
	return `strftime('%Y-%m-%dT%H:%M:%SZ','now')`
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

// tagJSONFilter returns an SQL fragment extracting the named JSON tag key
// from `tags`, plus a placeholder arg for the value. Branches json_extract
// (SQLite) vs `->>` (Postgres). Caller appends the value compare (`= ?` or
// ` IS NOT NULL`) and supplies the value arg if needed.
//
// SQLite shape: `json_extract(tags, ?)` with arg `"$.k"`.
// Postgres shape: `tags ->> ?` with arg `"k"`.
func (s *Store) tagJSONFilter(key string) (frag, arg string) {
	if s.driver == driverPostgres {
		return "tags ->> ?", key
	}
	return "json_extract(tags, ?)", "$." + key
}
