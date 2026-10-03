#!/usr/bin/env bash
# check-migrations.sh — guard SQLite ↔ Postgres migration parity.
#
# Both backends must converge to identical TABLE column sets. Drift is
# silent at runtime — a SQLite column added without the matching PG mirror
# breaks `disco serve` against PG once code references the new column.
#
# Approach: replay each migration set in file order (NNN_ prefixes sort
# lexically) into a `(table, column)` set — CREATE TABLE and ADD COLUMN add
# pairs, DROP COLUMN and DROP TABLE remove them — then sort and diff the final
# sets. Without the removals a column dropped on one side only would pass. The
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

# Awk replays the statements into a (table, column) set and prints it at END.
# Comments stripped, identifiers lowercased. RENAME is not modelled: a rename
# shows up as drift (loud), never as a silent pass.
extract_columns() {
  awk '
    BEGIN { tbl="" }
    # Strip line-level comments.
    { sub(/--.*$/, "") }
    # DROP TABLE [IF EXISTS] <name> — forget every column of that table.
    /^[[:space:]]*DROP[[:space:]]+TABLE/ {
      dline=$0
      sub(/^[[:space:]]*DROP[[:space:]]+TABLE([[:space:]]+IF[[:space:]]+EXISTS)?[[:space:]]+/, "", dline)
      split(dline, dp, /[[:space:]]+/)
      dtbl=tolower(dp[1])
      gsub(/[",;]/, "", dtbl)
      for (k in cols) { split(k, kp, " "); if (kp[1] == dtbl) delete cols[k] }
      next
    }
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
      cols[tbl " " col] = 1
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
      if (acol != "") cols[altbl " " acol] = 1
    }
    # DROP COLUMN [IF EXISTS] <col> — same attribution as ADD COLUMN.
    altbl != "" && /DROP[[:space:]]+COLUMN/ {
      xline=$0
      sub(/^.*DROP[[:space:]]+COLUMN[[:space:]]+/, "", xline)
      sub(/^IF[[:space:]]+EXISTS[[:space:]]+/, "", xline)
      split(xline, xp, /[[:space:]]+/)
      xcol=tolower(xp[1])
      gsub(/[",;]/, "", xcol)
      if (xcol != "") delete cols[altbl " " xcol]
    }
    END { for (k in cols) print k }
  '
}

tmp="$(mktemp -d)" || exit 2
trap 'rm -rf "$tmp"' EXIT

# A failed extraction is a tooling failure (exit 2), never "no drift".
cat "$SQLITE_DIR"/*.sql | extract_columns | sort -u > "$tmp/sqlite" || exit 2
cat "$PG_DIR"/*.sql      | extract_columns | sort -u > "$tmp/pg"     || exit 2

drift=0
if ! diff -u "$tmp/sqlite" "$tmp/pg" > "$tmp/diff"; then
  echo "migration drift detected (sqlite vs pg):" >&2
  cat "$tmp/diff" >&2
  drift=1
fi

if [[ $drift -eq 0 ]]; then
  echo "ok — sqlite + pg migrations have matching column sets"
fi
exit $drift
