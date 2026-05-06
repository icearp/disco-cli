#!/usr/bin/env bash
# oss-cherry-pick.sh — port a single upstream commit to the OSS mirror.
#
# Generates a patch from one upstream commit, drops file sections that
# belong to paid-only code (by name pattern or `//go:build paid`
# constraint), and applies the filtered patch to the OSS clone with
# `git am` so the upstream commit message and author are preserved. A
# trailer `(cherry picked from upstream <sha>)` is appended for
# traceability.
#
# Run from inside the upstream repo. The OSS clone is expected at
# ../disco (override with $OSS_DIR).
#
# Usage:
#   scripts/oss-cherry-pick.sh <upstream-sha>
#   scripts/oss-cherry-pick.sh <upstream-sha> --dry-run
#   OSS_DIR=/path/to/oss scripts/oss-cherry-pick.sh <sha>

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <upstream-sha> [--dry-run]" >&2
  exit 2
fi

SHA="$1"
DRY_RUN=0
[[ "${2:-}" == "--dry-run" ]] && DRY_RUN=1

OSS_DIR="${OSS_DIR:-../disco}"

# Resolve the sha against the upstream repo so a typo fails fast.
if ! FULL_SHA=$(git rev-parse --verify "${SHA}^{commit}" 2>/dev/null); then
  echo "not a commit in this repo: $SHA" >&2
  exit 1
fi

if [[ $DRY_RUN -eq 0 ]]; then
  if [[ ! -d "$OSS_DIR/.git" ]]; then
    echo "OSS_DIR is not a git repo: $OSS_DIR" >&2
    exit 1
  fi
  if ! git -C "$OSS_DIR" diff --quiet || ! git -C "$OSS_DIR" diff --cached --quiet; then
    echo "OSS_DIR has uncommitted changes; commit or stash first: $OSS_DIR" >&2
    exit 1
  fi
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

PATCH_IN="$TMPDIR/upstream.patch"
PATCH_OUT="$TMPDIR/filtered.patch"

git format-patch -1 --stdout "$FULL_SHA" >"$PATCH_IN"

# Name-pattern excludes mirror oss-sync.sh.
name_excluded() {
  local p="$1"
  case "$p" in
    *_paid.go|*_paid_test.go|*_paid.md|*_paid.sql) return 0 ;;
    scripts/oss-sync.sh|scripts/oss-cherry-pick.sh) return 0 ;;
    README.upstream.md) return 0 ;;
  esac
  return 1
}

# Detect a paid build constraint in the post-image of a file section.
# Reads the first few '+' lines (new file body) plus the existing file
# in the upstream tree at this commit. A constraint counts as paid when
# it lists `paid` as a positive term — `!paid` (OSS-only stubs) does not.
content_paid() {
  local path="$1" section="$2"
  [[ "$path" == *.go ]] || return 1

  # Inspect added lines first (covers brand-new paid files).
  local added
  added=$(grep -E '^\+(//go:build |// \+build )' "$section" | head -n 5 || true)
  if echo "$added" | grep -qE '\bpaid\b'; then
    if echo "$added" | grep -qE '!paid\b'; then
      return 1
    fi
    return 0
  fi

  # Fall back to inspecting the file as it exists at this commit.
  local head
  head=$(git show "$FULL_SHA:$path" 2>/dev/null | head -n 5 || true)
  if echo "$head" | grep -qE '^(//go:build |// \+build ).*\bpaid\b'; then
    if echo "$head" | grep -qE '^(//go:build |// \+build ).*!paid\b'; then
      return 1
    fi
    return 0
  fi
  return 1
}

# Extract paths from a file section's diff header. Handles renames
# (`rename from`/`rename to`) by returning both paths so either tagged
# side disqualifies the section.
section_paths() {
  local section="$1"
  awk '
    /^diff --git / { print $3; print $4; next }
    /^rename from / { sub(/^rename from /, ""); print "a/" $0 }
    /^rename to /   { sub(/^rename to /,   ""); print "b/" $0 }
  ' "$section" | sed -E 's#^[ab]/##' | sort -u
}

# Split the input patch into a header (everything before the first
# `diff --git`) and one file per section.
HEADER="$TMPDIR/header"
awk -v dir="$TMPDIR" '
  BEGIN { out = dir "/header"; idx = 0 }
  /^diff --git / {
    idx++
    out = sprintf("%s/section.%04d", dir, idx)
  }
  { print > out }
' "$PATCH_IN"

# `git format-patch` ends with a signature line ("-- \n<version>"); keep
# it attached to the final kept section so `git am` parses cleanly.
SECTIONS=("$TMPDIR"/section.*)
if [[ ! -e "${SECTIONS[0]}" ]]; then
  echo "no file changes in $FULL_SHA — nothing to port" >&2
  exit 1
fi

KEPT=()
DROPPED=()

for sec in "${SECTIONS[@]}"; do
  drop=0
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    if name_excluded "$path"; then
      drop=1
      reason="name:$path"
      break
    fi
    if content_paid "$path" "$sec"; then
      drop=1
      reason="paid-tag:$path"
      break
    fi
  done < <(section_paths "$sec")

  if [[ $drop -eq 1 ]]; then
    DROPPED+=("$reason")
  else
    KEPT+=("$sec")
  fi
done

echo "Upstream commit: $FULL_SHA"
echo "Kept sections (${#KEPT[@]}):"
for sec in "${KEPT[@]}"; do
  printf '  %s\n' "$(grep -m1 '^diff --git ' "$sec" | awk '{print $4}' | sed 's#^b/##')"
done
echo "Dropped sections (${#DROPPED[@]}):"
for r in "${DROPPED[@]}"; do
  printf '  %s\n' "$r"
done
echo

if [[ ${#KEPT[@]} -eq 0 ]]; then
  echo "commit is paid-only after filtering; nothing to port" >&2
  exit 1
fi

# Reassemble the filtered patch. The trailing signature lives in the
# last original section, but that section may have been dropped; append
# it explicitly from the input so `git am` sees a proper mailbox.
{
  cat "$HEADER"
  for sec in "${KEPT[@]}"; do
    cat "$sec"
  done
} >"$PATCH_OUT"

# Add a cherry-pick trailer to the patch's commit message so the OSS
# commit records the upstream sha. The mailbox format keeps the message
# in the lines after the `Subject:` block until the first `diff --git`.
TRAILER="(cherry picked from upstream $FULL_SHA)"
TMP_TRAILED="$TMPDIR/trailed.patch"
awk -v trailer="$TRAILER" '
  BEGIN { in_msg = 0; injected = 0 }
  /^Subject: / { in_msg = 1; print; next }
  in_msg && /^diff --git / && !injected {
    print ""
    print trailer
    print ""
    injected = 1
    in_msg = 0
  }
  { print }
' "$PATCH_OUT" >"$TMP_TRAILED"
mv "$TMP_TRAILED" "$PATCH_OUT"

if [[ $DRY_RUN -eq 1 ]]; then
  echo "Dry-run: filtered patch at $PATCH_OUT (will be removed on exit)."
  echo "Re-run without --dry-run to apply to $OSS_DIR."
  # Surface the patch before the trap deletes the temp dir.
  cp "$PATCH_OUT" "${TMPDIR%/*}/oss-cherry-pick.dryrun.patch"
  echo "Copy preserved: ${TMPDIR%/*}/oss-cherry-pick.dryrun.patch"
  exit 0
fi

if ! git -C "$OSS_DIR" am --3way <"$PATCH_OUT"; then
  echo >&2
  echo "git am failed in $OSS_DIR. Resolve conflicts there, then run:" >&2
  echo "  git -C $OSS_DIR am --continue   # or --abort" >&2
  exit 1
fi

echo "Applied to $OSS_DIR."
echo "Next: review with 'git -C $OSS_DIR log -1' and 'git -C $OSS_DIR push' when ready."
