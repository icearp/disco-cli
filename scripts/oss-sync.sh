#!/usr/bin/env bash
# oss-sync.sh — regenerate the OSS mirror tree from the upstream working copy.
#
# Strips every file whose first non-empty line is `//go:build paid` (and the
# matching `_paid.go` / `_paid_test.go` files by name) so the OSS repo never
# sees paid code. Run from inside the upstream repo. The OSS clone is
# expected at ../disco (override with $OSS_DIR).
#
# Usage:
#   scripts/oss-sync.sh                  # writes to $OSS_DIR, prints summary
#   scripts/oss-sync.sh --dry-run        # shows what would be excluded, no copy
#   OSS_DIR=/path/to/oss scripts/oss-sync.sh

set -euo pipefail

OSS_DIR="${OSS_DIR:-../disco}"
DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

if [[ $DRY_RUN -eq 0 && ! -d "$OSS_DIR" ]]; then
  echo "OSS_DIR not found: $OSS_DIR" >&2
  echo "Clone the OSS repo there first, or set OSS_DIR." >&2
  exit 1
fi

# Files excluded by name pattern.
NAME_EXCLUDES=(
  '*_paid.go'
  '*_paid_test.go'
  '*_paid.md'
  '*_paid.sql'
  # Postgres backend is paid-tagged; the migration files in this dir
  # are referenced only from migrate_pg_paid.go which never lands in
  # OSS. Exclude the whole subtree by glob so the dir doesn't ship.
  'migrations/pg'
  'migrations/pg/*'
  'scripts/oss-sync.sh'
  'scripts/oss-cherry-pick.sh'
  'README.upstream.md'
  'LICENSE'
  'LICENSE.md'
  'LICENSE.txt'
  '.git'
  'dist'
  'oss-mirror'
)

# Files excluded by content. A file is paid-only when its build constraint
# includes `paid` as a *positive* term (i.e. paid build is required). The
# OSS stub uses `//go:build !paid`, which must NOT match.
content_excluded() {
  local f="$1"
  [[ "$f" == *.go ]] || return 1
  local head_lines
  head_lines=$(head -n 5 "$f" 2>/dev/null)
  echo "$head_lines" | grep -qE '^//go:build .*\bpaid\b' || return 1
  # Reject if constraint is `!paid` (OSS-only).
  echo "$head_lines" | grep -qE '^//go:build .*!paid\b' && return 1
  return 0
}

# Build rsync exclude args.
rsync_args=()
for pat in "${NAME_EXCLUDES[@]}"; do
  rsync_args+=(--exclude="$pat")
done

# Find content-excluded files (paid build tag) and add per-file excludes.
mapfile -t paid_files < <(
  find . -type f -name '*.go' \
    -not -path './.git/*' \
    -not -path './dist/*' \
    -not -path './oss-mirror/*' \
    -print0 \
  | while IFS= read -r -d '' f; do
      if content_excluded "$f"; then
        # rsync paths are relative to source, drop leading "./"
        echo "${f#./}"
      fi
    done
)

for f in "${paid_files[@]}"; do
  rsync_args+=(--exclude="$f")
done

echo "Excluded by name pattern:"
printf '  %s\n' "${NAME_EXCLUDES[@]}"
echo
echo "Excluded by //go:build paid (${#paid_files[@]} files):"
printf '  %s\n' "${paid_files[@]}"
echo

if [[ $DRY_RUN -eq 1 ]]; then
  echo "Dry-run: no copy performed."
  exit 0
fi

rsync -a --delete \
  "${rsync_args[@]}" \
  ./ "$OSS_DIR/"

echo "Synced to $OSS_DIR."
echo "Next: cd $OSS_DIR && git add -A && git commit && git push"
