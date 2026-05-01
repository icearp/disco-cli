#!/usr/bin/env bash
# aws-next-service.sh — emit next AWS service to scaffold + its uncovered CFN types.
#
# Sorts uncovered services by type-count desc (biggest gain first), drops any
# service listed in docs/aws-skip.md (untracked, see .gitignore), prints the
# top service and its uncovered resource types.
#
# Usage:
#   scripts/aws-next-service.sh             # next service (head of queue)
#   scripts/aws-next-service.sh --list 10   # top 10 services by uncovered count
#   scripts/aws-next-service.sh SageMaker   # uncovered types for given service

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

skip_file="docs/aws-skip.md"
skip_filter='cat'
if [[ -f "$skip_file" ]]; then
  # Lines of form "- AWS::Service::Type" treated as skipped types. Use awk
  # exact-match against column 2 (the CFN type) — substring match would
  # over-filter (e.g. "AWS::EC2::Route" eating RouteServer*).
  skip_filter="awk -F'\\t' 'NR==FNR{skip[\$2]=1;next} !(\$2 in skip)' <(awk '/^- AWS::/ {print \"x\\t\"\$2}' $skip_file) -"
fi

cov="$(mktemp)"
trap 'rm -f "$cov"' EXIT

CGO_ENABLED=0 go run . coverage --provider aws --output table 2>/dev/null > "$cov"

uncovered_rows() {
  awk 'NR>2 && $NF=="uncovered" {print $2"\t"$(NF-1)}' "$cov" \
    | eval "$skip_filter"
}

if [[ "${1:-}" == "--list" ]]; then
  n="${2:-20}"
  uncovered_rows | awk -F'\t' '{print $1}' | sort | uniq -c | sort -rn | head -"$n"
  exit 0
fi

if [[ -n "${1:-}" ]]; then
  uncovered_rows | awk -F'\t' -v s="$1" '$1==s {print $2}'
  exit 0
fi

top="$(uncovered_rows | awk -F'\t' '{print $1}' | sort | uniq -c | sort -rn | awk 'NR==1 {print $2}')"
if [[ -z "$top" ]]; then
  echo "all services covered" >&2
  exit 0
fi

count="$(uncovered_rows | awk -F'\t' -v s="$top" '$1==s' | wc -l)"
echo "# next: $top ($count uncovered types)"
uncovered_rows | awk -F'\t' -v s="$top" '$1==s {print $2}'
