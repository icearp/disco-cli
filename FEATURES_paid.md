# Disco Paid Features

Shipped, in-tree paid surface. Closed-source — excluded from OSS sync via `scripts/oss-sync.sh` (`*_paid.md` name pattern). Do not reference these items from any OSS-tracked file (commits, CLAUDE.md, README, OSS `FEATURES.md` / `ROADMAP.md`).

Companion to `ROADMAP_paid.md` (forward-looking paid plans).

---

## License gate

`internal/license/license_paid.go` + OSS stub at `internal/license/license.go`. `license.Require()` is the canonical entry-call every paid command runs as the first line of `RunE`. OSS build gets a no-meaning stub that never authorizes; paid build validates the license envelope. Pattern documented in `CLAUDE.md` (OSS / paid split).

## `disco diff <scanA> <scanB>` — drift detection

Paid command in `cmd/diff_paid.go`. Walks two scan timestamps and emits added / removed / changed resources.

Filters:
- `--type` — disco type (`aws:ec2:instance`, etc.)
- `--provider` — `aws|azure|gcp`
- `--kind added|removed|changed` — restrict result class
- `--region`
- `--account`

Pairs with G10 (`disco scan --resume`, partial — checkpoint persistence shipped OSS-side; resumable consumer remains in `ROADMAP_paid.md`) and L6 (continuous mode, planned).

## `disco check --persist` — findings persistence

Paid override of OSS `disco check` via the var-function reassignment pattern (`cmd/check_paid.go` + `cmd/helpers_paid.go` reassign in `init()`; OSS file ships the no-op default). When `--persist` is set, every finding produced by Rego eval lands in the persistent tables defined by migration `004_findings_paid.sql`:

- `check_runs` — one row per `disco check --persist` invocation (timestamp, packs, scope filters).
- `findings` — per-finding rows with severity, rule ID, message, resource ID, run-id FK (CASCADE on run delete).

The schema migration ships paid-only — name pattern `*_paid.sql` excludes from OSS sync. Upstream OSS dev-builds embed and apply it; published OSS mirror never sees it.

## `disco findings list` / `disco findings runs` — read commands

`cmd/findings_paid.go` exposes two read verbs over the persisted store:

- `disco findings runs` — list `check_runs` rows.
- `disco findings list` — list findings, optionally scoped to a run-id. Carries `--since` filter (matches the OSS `--since` shape on `list`).

Both gated behind `license.Require()`.

## Findings persistence schema

`internal/store/findings_paid.go` + `internal/store/migrations/004_findings_paid.sql` define the storage layer:

- Tables: `check_runs`, `findings` (FK CASCADE).
- Indices on `(run_id, severity)` and `(resource_id)` for the common query shapes.
- Schema additive — empty in OSS builds since no `--persist` flag is registered.

Future paid follow-ups (`disco findings diff`, retention pruning, drift heatmaps, ticket sync) build atop this same schema. See `ROADMAP_paid.md` focus-group follow-ups for the planned surface.
