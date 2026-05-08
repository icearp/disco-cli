# Disco Paid Feature Roadmap

Closed-source roadmap. Excluded from OSS sync via `scripts/oss-sync.sh` (`*_paid.md` name pattern). Do not reference these items from any OSS-tracked file (commits, CLAUDE.md, README, OSS ROADMAP).

Tier headings mirror the OSS roadmap (NEXT — quarter; LATER — 6–12mo / v1.0) so cross-comparison stays readable.

Shipped paid surface lives in `FEATURES_paid.md`.

---

## Focus-group follow-ups

Paid deferrals from the focus-group remediation cycle (F1–F21 in `focus-group/SUMMARY.md`). All F1–F21 findings shipped; items below are net-new paid follow-up plans atop the shipped foundation. OSS-eligible deferrals live in `ROADMAP.md`.

- **`disco snapshot --sign` + `disco verify --signature`** — cosign / Sigstore non-repudiation atop the OSS unsigned-archive shape. Signs the single-file archive blob (aligns with cosign attest convention); verify validates against a configured trust root before running the existing inner-DB hash check. Where the real evidence-package differentiator lives.
- **`disco findings diff <runA> <runB>`** — added / resolved / persistent finding sets between two `check_runs` rows. Builds atop the F21-slice-2 schema; pairs with the existing `disco diff <scanA> <scanB>` resource-drift verb.
- **Findings retention policy** — `disco findings rm <run-id>` (single-run) and time-window pruning (`disco findings rm --older-than 90d`). FK CASCADE already in place; needs CLI surface + dry-run flag.
- **Drift heatmaps / dashboards** — render-side aggregation atop persisted `findings`. Severity-by-pack, finding-by-resource-type, time-window heatmap. Fits into a future `disco serve` (paid L3) or stand-alone `disco findings report`.
- **Ticket sync** — `disco findings sync --jira` / `--linear` / `--github`. New high-severity finding → ticket. Configured per-pack via Rego rule metadata (`tags.ticket.priority` etc.).
- **`disco snapshot s3://...`** — remote-upload sink for snapshot archives. Same precedent as L5 (SIEM/SOAR sinks). Local-fs path stays OSS; remote sinks paid.

---

## NEXT — this quarter

### G10. `disco scan --resume` — incremental / resumable scans (paid feature)
N1 PartialScan landed the status flag; OSS-side `scan_checkpoints` (migration `002_scan_checkpoints.sql`) already persists per-(scan, provider, service, scope) continuation tokens. The paid incremental scanner consumes those checkpoints on `disco scan --resume <scanID|latest>` to skip already-listed pages — natural follow-up to PartialScan, driven by big-account scan timeouts. Engine in `internal/scanresume/` (paid); CLI dispatch via `startOrResumeScan` in `cmd/scan.go` (already in OSS, currently a no-op for the consumer side).

---

## LATER — 6–12mo / v1.0

### L2. Pluggable store backend — SHIPPED 2026-05-08
- Single `*Store` learns Postgres in addition to SQLite (paid-tagged). pgx via `pgx/v5/stdlib` reuses the existing sqlx code path; dialect bits branch on `s.driver` in `internal/store/dialect.go`. Migrations in `internal/store/migrations/pg/*.sql` mirror the SQLite set plus `005_tenant_id_rls.sql` for per-table RLS. Tenant pinning at process start via pgconn `AfterConnect`. `make check-migrations` guards SQLite ↔ PG column-set parity.
- See `FEATURES_paid.md` § Postgres backend.
- Decision diff vs original plan: rejected the `ReadBackend`/`WriteBackend` interface split — single struct + driver branch is simpler and the SaaS app imports `internal/store` directly (no abstraction needed). Hand-rolled migration runner instead of golang-migrate per CLAUDE.md rule 7 (minimize deps).

### L3. API server mode — REMOVED 2026-05-08
- `disco serve` shipped 2026-05-08 as a 2-route HTTP API (`POST /v1/scans`, `GET /v1/healthz`) and was removed the same day after architecture review. In the Fargate-per-scan deploy shape (Lambda → ECS RunTask → one-shot container), every problem the HTTP layer solved was already solved by the architecture itself: scope is known at RunTask time (no misroute attack vector), container is single-use (no multi-request listener needed), Lambda is the only caller (no need for a typed API surface).
- Replaced with: Lambda invokes `ecs:RunTask` with `containerOverrides.command = ["scan", ...]`. Container `ENTRYPOINT` is `/disco`. Existing CLI surface drives the scan; existing `*store.Store` PG path persists results.
- See `FEATURES_paid.md` § Scan worker deploy.
- Net deletion: ~700 LOC (`cmd/serve_paid.go`, `internal/serve/*`).
- Future API surface (read endpoints, MCP, GraphQL) would be a fresh design driven by an actual external consumer, not a continuation of the v1 serve attempt.

### L4. Web UI (paid feature, separate repo)
- Graph visualizer + rule results. Consumes L3 API.

### L5. SIEM/SOAR sinks (paid feature)
- `disco export --sink splunk|elastic|sentinel|panther`. Resources + findings.

### L6. Continuous mode (paid feature)
- `disco watch` — cron-driven scan + diff + webhook on drift. Local daemon, systemd unit.

### L7. Compliance framework Rego packs (paid feature)
- `disco check` command + Rego engine (`internal/policy/`) ship in OSS — engine, BYO `--rules` flow, and Conftest-compatible `data.disco.deny` shape are all free. Paid value-add: curated, audited first-party policy bundles for compliance teams.
- Target packs: NIST SP 800-53, CIS Benchmarks (AWS / Azure / GCP Foundations), PCI-DSS v4, AWS / Azure / GCP Well-Architected pillars. Each pack is a Rego module set populating `data.disco.deny` with the existing `Finding` shape; severity calibrated; mapped via `tags.compliance.control` (e.g. `CIS-AWS-1.20`, `NIST-AC-2`). Remediation copy + ref URLs included.
- Distribution: embedded in the paid binary via `embed.FS`. New `--pack <name>` flag (additive to `--rules`) selects bundled packs by name; `--pack list` enumerates available bundles. Behind `//go:build paid`, license-gated at command entry — engine runs unmodified.
- Engine follow-ups (any tier): (a) `disco.relationships_from/_to` Rego builtins wired to `store.RelationshipsFrom/To`; (b) per-rule ID filter once a stable convention lands; (c) `--format sarif` for GitHub/GitLab integration (tracked in OSS G7).

### L10. Remote MCP server (paid feature)
- `disco mcp serve` — Model Context Protocol server exposing the resource graph, rule findings, and edge traversal as tools an AI agent (Claude, ChatGPT, IDE assistants) can call. Lets users query "blast radius from compromised role X" or "find every internet-exposed RDS in prod" via natural language without bespoke glue.
- Tool surface: `list_resources(filter)`, `get_resource(id)`, `graph_blast(id, depth)`, `graph_path(from, to)`, `check_findings(rule, severity, tag)`, `coverage_matrix()`. Read-only — no scan-trigger, no DB writes.
- Transport: stdio for local IDE integrations + SSE/HTTP for hosted deployments. Auth via license token + per-tenant API key on the HTTP path.
- Pairs with L3 (API server) — the MCP layer is a thin adapter over the same read-only REST surface, so both consume the L2 store interface uniformly. Pairs with L4 (Web UI) for human-facing parity.
- Pricing rationale: the MCP layer multiplies disco's value for AI-augmented SecOps without exposing scan internals; gates the surface that customers will integrate into their LLM workflows.

### L11. IaC drift (paid feature)
- `disco drift <iac-source>` — compare the live cloud scan against the declared infrastructure state. Sources:
  - Terraform: `terraform show -json <plan|state>` consumed via `tfjson` schema; resource address → cloud ARN/ID via `id` attribute (most providers' tfstate carries it; rest derive via type-specific projection).
  - CloudFormation: walk every `aws:cloudformation:stack` resource's `StackResourceSummary.PhysicalResourceId` (already scanned) — diff against in-store `aws:*` rows. Re-uses `cfnTypeMap` from `cloudformation_resolvers.go` for type → disco-type translation.
  - Bicep / ARM: parse deployment JSON for `Microsoft.<Provider>/<type>` + resource name, lookup in `azureAPITypeMap`.
- Output kinds: `unmanaged` (in cloud, not in code — shadow infra), `missing` (in code, not in cloud — failed deploy), `drifted` (in both, attribute mismatch). Severity gradient maps to `Finding.Severity` so existing `disco check` consumers reuse the surface.
- Pairs with G6 (`disco diff`) — temporal drift between two scans vs. policy drift between scan and code. Output formats overlap: `--output table|json|sarif`.
- Pricing rationale: drift answers "what's in cloud but not in our IaC" — the canonical posture-and-compliance question that drives platform-team adoption. Rule-engine queries (CIS / NIST / PCI tags) compose with drift filters: "find unmanaged S3 buckets that fail CIS 2.2".
- Out of scope for v1: bidirectional reconciliation, plan-mode dry-run, multi-source merge (Terraform + CloudFormation in one repo). Single source per invocation.

### L9. MITRE ATT&CK assessment (paid feature)
- Map discovered resources + edges to ATT&CK Cloud matrix techniques (T1078 Valid Accounts, T1190 Exploit Public-Facing App, T1526 Cloud Service Discovery, T1530 Data from Cloud Storage Object, T1537 Transfer Data to Cloud Account, T1580 Cloud Infrastructure Discovery, T1538 Cloud Service Dashboard, T1098.001/.003 Account Manipulation, T1199 Trusted Relationship, T1496 Resource Hijacking, etc.).
- Implemented via `disco check` — no new top-level command. Reuses rule eval + existing `Related` graph traversal (already single-hop with nestable depth).
- DSL extension: add `Tags map[string][]string` to `Rule` (free-form key→values). ATT&CK rules carry `tags: { attack.technique: [T1530], attack.tactic: [collection], attack.platform: [iaas] }`. Tag keys/values arbitrary — engine treats as opaque metadata, propagates onto `Finding`.
- `disco check --format attack-navigator` groups findings by `attack.technique` tag, emits ATT&CK Navigator JSON layer. `--format json` includes raw tags. Other tag keys (`compliance.control: [CIS-1.20]`, `owasp: [A05]`) free-ride on same mechanism.
- Graph-derived techniques (T1199 cross-account roles, T1078 lateral IAM, T1530 public-exposure) authored as rules with nested `Related` blocks — no engine changes needed.
- Cross-provider: AWS/Azure/GCP rules unified under cloud sub-matrices (IaaS, SaaS, Office 365, Azure AD, Google Workspace) via `attack.platform` tag.
- Ship as curated paid rule pack (`rules/attack/*.yaml`) gated behind license. OSS users can author their own tagged rules; navigator output formatter is the paid surface.
- Pairs with L5 (SIEM sinks) — push technique coverage to detection platforms. Pairs with L7 (Rego) — Rego rules emit same tag shape.
