# Decouple disco from disco-saas + Open-Source — High-Level Plan

> Status: high-level / parent plan. Each phase below is meant to be expanded into
> its own detailed sub-plan before execution.

## Context

`disco` (this repo, module `codeberg.org/icearp/disco`) is a cloud-resource
discovery CLI. A sibling private repo, `disco-saas`
(`codeberg.org/icearp/disco-saas`), is a multi-tenant control plane that:

- imports **only** the `store` package from this repo (specifically the *paid*
  Postgres multi-tenant backend: `OpenPostgresInSchema`, `OpenPostgresInSchemaWithWorkspace`,
  `WrapTx`, RLS via `app.tenant_id` / `app.workspace_id` GUCs), and
- couples at build time via `replace codeberg.org/icearp/disco => ../disco-upstream`
  + `-tags paid` (the two repos must sit side-by-side; see `disco-saas/Dockerfile`,
  `disco-saas/go.mod`).
- invokes the `disco` binary as a scan worker (ECS Fargate / local subprocess) via
  an env-var contract: `DISCO_PG_DSN`, `DISCO_PG_SCHEMA`, `DISCO_TENANT_ID`,
  `DISCO_WORKSPACE_ID`, `DISCO_SCAN_ID`, `DISCO_PG_IAM_AUTH`, `DISCO_PG_MAX_CONNS`.

Today, "what is OSS vs paid" is enforced by a build-tag split (`*_paid.go` +
`//go:build paid`) with an `oss-sync.sh` mirror generator. The cloud/infra is
otherwise clean: deps are all public, no secrets in source, CGO disabled.

### Decisions (locked)

| Decision | Choice |
|---|---|
| End-state topology | **Open-source everything** — no paid tier. One public repo; disco-saas becomes a thin private consumer. |
| Public module path | **Keep `codeberg.org/icearp/disco`** — no import rename. |
| License | **MIT** |

### Intended outcome

1. `disco-upstream` becomes a clean, fully-OSS public repo: no build tags, no
   `oss-sync` machinery, no SaaS-internal docs, MIT-licensed.
2. `disco-saas` no longer depends on this repo's working tree — it consumes
   `codeberg.org/icearp/disco @ vX.Y.Z` as a normal tagged module and builds
   without `-tags paid`.
3. The env-var scan-worker contract is preserved (no behavioral change for the
   running SaaS), just relocated into now-OSS code.

---

## Phase 0 — Pre-flight (verify before touching anything)

- Confirm disco-saas imports **nothing** from disco besides `store` (re-grep
  `disco-saas` for `codeberg.org/icearp/disco/`). Exploration says store-only;
  re-verify so de-tagging can't strand a paid symbol saas needs.
- Inventory the exact symbols saas consumes so they survive de-tagging intact:
  `store.OpenPostgresInSchema`, `OpenPostgresInSchemaWithWorkspace`, `WrapTx`,
  `DriverPostgres`, `Rel*`/`Dir*` constants, `GraphWalkOpts`/`GraphPathOpts`,
  `ResourceFilter`. (All currently in `store/postgres_paid.go` + untagged store files.)
- Snapshot current green state: `go test ./...` **and** `go test -tags paid ./...`
  both pass; `make check-migrations` passes. This is the regression baseline.

## Phase 1 — Collapse the OSS/paid build-tag split (the core of "open-source everything")

Goal: there is no longer a `paid` build tag anywhere. The default build == the
former paid build.

1. **De-tag source files** — for every `*_paid.go` / `*_paid_test.go`
   (~32 files across `store/`, `cmd/`, `internal/license/`):
   rename to drop the `_paid` suffix, delete the `//go:build paid` line.
   Representative files: `store/postgres_paid.go`, `store/migrate_pg_paid.go`,
   `store/pgiam_paid.go`, `store/findings_paid.go`, `store/diff_paid.go`,
   `store/resources_*_paid.go`, `cmd/diff_paid.go`, `cmd/findings_paid.go`,
   `cmd/check_paid.go`, `cmd/helpers_paid.go`.
2. **Fold the var-function override pattern** — files like `cmd/check_paid.go`
   reassign a `var fn = ...` in `init()` to inject paid behavior over an OSS
   default. Collapse each into a single untagged definition (the former paid body
   becomes the only body). Remove the now-dead OSS-default closures.
3. **Remove the license gate** — delete `internal/license/` (both the `!paid`
   stub `license.go` and `license_paid.go`); strip the
   `if err := license.Require(); err != nil { return err }` first line from every
   paid command's `RunE` (`cmd/diff.go`, `cmd/findings.go`, `cmd/check.go`).
4. **Migrations** — drop `_paid` from migration filenames
   (`store/migrations/002_findings_paid.sql` → `002_findings.sql`,
   `006_resource_versioning_paid.sql` → `006_resource_versioning.sql`) and merge
   the `store/migrations/pg/` set in as first-class (no longer mirror-excluded).
   Re-baseline `make check-migrations` parity (the PG-only `tenant_id` allowlist
   stays — it's a real schema difference, not a paid artifact).
5. **Drop the mirror tooling** — delete `scripts/oss-sync.sh`,
   `scripts/oss-cherry-pick.sh`, and `README.upstream.md`.
6. **CI** — collapse the dual `go test ./...` + `go test -tags paid ./...` matrix
   into a single untagged run; drop `make build-paid` / `make test-paid` targets
   from the `Makefile`.
7. **Verify**: `go test ./...` green; `go vet`, `golangci-lint`, `gofmt -w .`
   clean. Confirm pgx/diff/findings now link into the default binary
   (`strings disco | grep pgx` non-empty; `disco diff --help` / `disco findings --help`
   work without a license error).

## Phase 2 — Purge SaaS-internal references from the now-public tree

- Delete SaaS-internal planning/handoff docs: `PLAN_saas_paid.md`,
  `PLAN_saas_prereqs_paid.md`, `PLAN_saas_schema_per_tenant_paid.md`,
  `docs/saas-handoff_paid.md`, `FEATURES_paid.md`, `ROADMAP_paid.md`.
- Scrub `disco-saas` mentions from code comments + Dockerfile:
  `Dockerfile` (the `disco-saas/scanner:dev` build-context comments),
  `internal/providers/aws/aws_config.go`, `internal/providers/gcp/wif.go`,
  `cmd/CLAUDE.md`, `store/CLAUDE.md`. Reword to provider-neutral language
  ("an external orchestrator", "a Postgres-per-tenant deployment") — the env-var
  contract stays; only the proprietary-product naming goes.
- Decide the multi-tenant schema-name validation in `store` (the
  `tenant_[0-9a-f]{32}` regex in the former `postgres_paid.go`): keep it as a
  documented convention (saas still relies on it) but reword the comment to not
  reference disco-saas. **Do not loosen** without checking saas's provisioner.

## Phase 3 — OSS hygiene / licensing / docs

- **LICENSE**: add MIT `LICENSE` at repo root (drop the `oss-sync.sh` LICENSE
  exclusion — now it's tracked and shipped). Year + "Dick Childress" (confirm
  copyright holder).
- **README.md**: rewrite to describe the single OSS product. Remove any
  paid-vs-OSS feature distinction (no longer exists); document `scan/list/diff/
  graph/check/findings/snapshot/verify` as one feature set. Update the SARIF
  tool URL in `cmd/check_sarif.go` if needed (already `codeberg.org/icearp/disco`).
- **FEATURES.md / ROADMAP.md**: strip stale internal-module + SaaS references;
  reframe ROADMAP as the public roadmap.
- **Community health**: add `CONTRIBUTING.md` (dev branch model, CGO_ENABLED=0,
  test/lint expectations), `SECURITY.md` (disclosure contact), `CODE_OF_CONDUCT.md`
  (Contributor Covenant), and Forgejo issue/PR templates. *(nice-to-have; can be
  a fast-follow.)*
- Re-evaluate the per-dir `CLAUDE.md` files: keep (useful to contributors) but
  remove the OSS/paid-split sections that no longer apply.

## Phase 4 — Decouple disco-saas from this repo's working tree

*(Changes here land in the `disco-saas` repo, not this one.)*

- Cut a tagged release of disco (`vX.Y.0`) from the post-Phase-3 tree so saas has
  a versioned module to pin.
- In `disco-saas/go.mod`: drop `replace codeberg.org/icearp/disco => ../disco-upstream`;
  require `codeberg.org/icearp/disco vX.Y.0`. `go mod tidy`.
- In `disco-saas/Dockerfile`: remove the `--build-context upstream=../disco-upstream`
  wiring and the `COPY --from=upstream . /disco-upstream`; drop `-tags paid` from
  the `go build` (the tag no longer exists). The module now downloads from the
  public repo like any dep.
- Verify saas builds + tests green against the published module; verify the
  scan-worker image still satisfies the env-var contract.

## Phase 5 — Publish

- Flip the Codeberg repo (or a dedicated public mirror under `icearp`) to public.
  History note: full git history contains the old `*_paid` files. **Decide**:
  (a) accept history as-is (paid code was always slated for OSS, so low risk), or
  (b) publish from a squashed "initial OSS release" commit / `git filter-repo`
  cleanup for a tidy history. Recommend (a) unless a specific commit is sensitive.
- Confirm the existing `.forgejo/workflows/release.yaml` (Forgejo release API)
  still produces cross-platform binaries from the public repo.
- Announce / set repo description, topics, etc.

---

## Verification (end-to-end)

1. **disco (this repo)**: `CGO_ENABLED=0 go test ./...`, `go vet ./...`,
   `golangci-lint run`, `make check-migrations` — all green with **no** `-tags paid`
   anywhere. `grep -rn 'go:build paid\|_paid\|disco-saas\|license.Require' .`
   returns nothing in tracked files.
2. **Binary smoke**: `disco scan`, `disco diff`, `disco findings list`,
   `disco check --persist`, `disco snapshot`/`verify` all run on the default build.
3. **Postgres path**: with `DISCO_PG_DSN` + `DISCO_TENANT_ID` set, `disco scan`
   writes to the per-tenant schema (the former paid behavior, now default).
4. **disco-saas**: builds with no replace directive and no `-tags paid`; its
   `store`-package call sites compile against the published module; integration
   tests green; a local-mode scan still writes per-tenant rows.

## Sequencing notes

- Phases 1→3 are all in this repo and should land as a series of focused commits
  (one workstream per commit per the repo's commit ritual): de-tag, purge-saas,
  license+docs.
- Phase 4 cannot start until a disco release tag exists (end of Phase 3).
- Phase 5 (publish) is last and is largely ops, not code.
- Biggest risk is Phase 1 step 2 (folding the var-override pattern) silently
  dropping coverage — keep `go test ./...` green after each file.
