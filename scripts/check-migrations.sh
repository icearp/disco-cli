#!/usr/bin/env bash
# check-migrations.sh — guard SQLite ↔ Postgres migration parity.
#
# Both backends must converge to identical TABLE column sets. Drift is
# silent at runtime — a SQLite column added without the matching PG mirror
# breaks `disco serve` against PG once code references the new column.
#
# Approach: extract `(table, column)` pairs from each migration set's
# CREATE TABLE / ALTER TABLE ADD COLUMN statements, sort, diff. Allow a
# small ignore list for PG-only columns (currently `tenant_id` for RLS).
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
    # ALTER TABLE <name> ADD COLUMN <col> ...
    /^[[:space:]]*ALTER[[:space:]]+TABLE/ {
      line=$0
      sub(/^[[:space:]]*ALTER[[:space:]]+TABLE[[:space:]]+/, "", line)
      n=split(line, parts, /[[:space:]]+/)
      if (n < 4) next
      atbl=tolower(parts[1])
      # Look for ADD COLUMN
      for (i=2; i<n; i++) {
        if (toupper(parts[i]) == "ADD" && toupper(parts[i+1]) == "COLUMN") {
          acol=tolower(parts[i+2])
          gsub(/[",]/, "", acol)
          print atbl, acol
          break
        }
      }
    }
  '
}

cat "$SQLITE_DIR"/*.sql | extract_columns | sort -u > /tmp/disco-cols-sqlite.$$
cat "$PG_DIR"/*.sql      | extract_columns | sort -u > /tmp/disco-cols-pg.$$

# PG-only columns we explicitly accept (tenant_id, RLS-related).
PG_ONLY_ALLOWLIST="tenant_id"

# Drop allowlisted PG-only cols before diff.
grep -vE " ($PG_ONLY_ALLOWLIST)$" /tmp/disco-cols-pg.$$ > /tmp/disco-cols-pg-stripped.$$ || true

drift=0
if ! diff -u /tmp/disco-cols-sqlite.$$ /tmp/disco-cols-pg-stripped.$$ > /tmp/disco-cols-diff.$$; then
  echo "migration drift detected (sqlite vs pg, after PG-only allowlist):" >&2
  cat /tmp/disco-cols-diff.$$ >&2
  drift=1
fi

rm -f /tmp/disco-cols-sqlite.$$ /tmp/disco-cols-pg.$$ /tmp/disco-cols-pg-stripped.$$ /tmp/disco-cols-diff.$$

if [[ $drift -eq 0 ]]; then
  echo "ok — sqlite + pg migrations have matching column sets"
fi
exit $drift
