#!/usr/bin/env bash
# check-migrations.sh — guard SQLite ↔ Postgres migration parity.
#
# Both backends must converge to identical TABLE column sets. Drift is
# silent at runtime — a SQLite column added without the matching PG mirror
# breaks `disco serve` against PG once code references the new column.
#
# Approach: extract `(table, column)` pairs from each migration set's
# CREATE TABLE / ALTER TABLE ADD COLUMN statements, sort, diff. The OSS
# schema is single-tenant, so the two sets must match exactly — the SaaS
# multi-tenant columns (tenant_id, RLS plumbing) live in disco-saas's own
# migration set, not here.
#
# Exits 0 on parity, 1 on drift, 2 on tooling failure.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
SQLITE_DIR="$REPO_ROOT/store/migrations"
PG_DIR="$REPO_ROOT/store/migrations/pg"

if [[ ! -d "$PG_DIR" ]]; then
  echo "no PG migrations dir at $PG_DIR" >&2
  exit 2
fi

# Awk extracts (table, column) pairs from CREATE TABLE blocks + ALTER ADD
# COLUMN statements. Comments stripped, identifiers lowercased.
extract_columns() {
  awk '
    BEGIN { tbl="" }
    # Strip line-level comments.
    { sub(/--.*$/, "") }
    # CREATE TABLE [IF NOT EXISTS] <name> (
    /^[[:space:]]*CREATE[[:space:]]+TABLE/ {
      altbl=""
      sub(/^[[:space:]]*CREATE[[:space:]]+TABLE([[:space:]]+IF[[:space:]]+NOT[[:space:]]+EXISTS)?[[:space:]]+/, "")
      sub(/[[:space:]]*\(.*$/, "")
      tbl=tolower($0)
      sub(/[[:space:]]+$/, "", tbl)
      next
    }
    # End of CREATE TABLE block.
    /^\)/ { tbl=""; next }
    /^[[:space:]]*\);/ { tbl=""; next }
    # Inside CREATE TABLE: column lines start with identifier + type.
    tbl != "" {
      line=$0
      sub(/^[[:space:]]+/, "", line)
      # Skip constraint-only lines (PRIMARY KEY, FOREIGN KEY, UNIQUE, CHECK).
      if (line ~ /^(PRIMARY[[:space:]]+KEY|FOREIGN[[:space:]]+KEY|UNIQUE|CHECK|CONSTRAINT)/) next
      # Take the first token as column name.
      n=split(line, parts, /[[:space:]]+/)
      if (n < 2) next
      col=tolower(parts[1])
      gsub(/[",]/, "", col)
      if (col == "" || col ~ /^\(/) next
      print tbl, col
    }
    # ALTER TABLE <name> ... — capture the table. ADD COLUMN may be on this
    # line or a following one: Postgres uses `ALTER TABLE\n  ADD COLUMN ...`
    # (single statement, several columns, often `IF NOT EXISTS`).
    /^[[:space:]]*ALTER[[:space:]]+TABLE/ {
      altline=$0
      sub(/^[[:space:]]*ALTER[[:space:]]+TABLE[[:space:]]+/, "", altline)
      split(altline, ap, /[[:space:]]+/)
      altbl=tolower(ap[1])
      gsub(/[",;]/, "", altbl)
    }
    # ADD COLUMN [IF NOT EXISTS] <col> — attributed to the most-recent ALTER
    # TABLE. Fires on its own line (multi-line PG form) or inline (SQLite form).
    altbl != "" && /ADD[[:space:]]+COLUMN/ {
      cline=$0
      sub(/^.*ADD[[:space:]]+COLUMN[[:space:]]+/, "", cline)
      sub(/^IF[[:space:]]+NOT[[:space:]]+EXISTS[[:space:]]+/, "", cline)
      split(cline, cp, /[[:space:]]+/)
      acol=tolower(cp[1])
      gsub(/[",;]/, "", acol)
      if (acol != "") print altbl, acol
    }
  '
}

cat "$SQLITE_DIR"/*.sql | extract_columns | sort -u > /tmp/disco-cols-sqlite.$$
cat "$PG_DIR"/*.sql      | extract_columns | sort -u > /tmp/disco-cols-pg.$$

drift=0
if ! diff -u /tmp/disco-cols-sqlite.$$ /tmp/disco-cols-pg.$$ > /tmp/disco-cols-diff.$$; then
  echo "migration drift detected (sqlite vs pg):" >&2
  cat /tmp/disco-cols-diff.$$ >&2
  drift=1
fi

rm -f /tmp/disco-cols-sqlite.$$ /tmp/disco-cols-pg.$$ /tmp/disco-cols-diff.$$

if [[ $drift -eq 0 ]]; then
  echo "ok — sqlite + pg migrations have matching column sets"
fi
exit $drift
