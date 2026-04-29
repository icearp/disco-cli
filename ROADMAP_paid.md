# Disco Paid Feature Roadmap

Closed-source roadmap. Excluded from OSS sync via `scripts/oss-sync.sh` (`*_paid.md` name pattern). Do not reference these items from any OSS-tracked file (commits, CLAUDE.md, README, OSS ROADMAP).

Tier headings mirror the OSS roadmap (NEXT — quarter; LATER — 6–12mo / v1.0) so cross-comparison stays readable.

---

## NEXT — this quarter

### G3. Incremental / resumable scans (paid feature)
(Moved from prior roadmap — still outstanding.) Per-service watermark, `disco scan --resume <scanID>`. Migration `002_service_progress.sql`, `internal/store/scans.go`, `cmd/scan.go`.

### G6. `disco diff` (paid feature)
Gate `disco diff <scanA> <scanB>` behind license check. Drift detection across two scan timestamps is the primary paid value-add for compliance teams running scheduled scans — pairs with G3 (incremental scans) and L6 (continuous mode). Base `--type`, `--provider`, `--kind added|removed|changed`, `--region`, `--account` filters ship as part of the paid surface. License check shared with other paid commands; OSS build returns "diff requires a license" with link.

---

## LATER — 6–12mo / v1.0

### L2. Pluggable store backend (paid feature)
- SQLite → Postgres driver for team-shared DB. `internal/store/` becomes interface + two implementations. Keep SQLite default.

### L3. API server mode (paid feature)
- `disco serve` — read-only REST + gRPC over store. Enables dashboards, Slackbots, CI gates.

### L4. Web UI (paid feature, separate repo)
- Graph visualizer + rule results. Consumes L3 API.

### L5. SIEM/SOAR sinks (paid feature)
- `disco export --sink splunk|elastic|sentinel|panther`. Resources + findings.

### L6. Continuous mode (paid feature)
- `disco watch` — cron-driven scan + diff + webhook on drift. Local daemon, systemd unit.

### L7. Policy-as-code (OPA/Rego) (paid feature)
- Alternative to G4 for teams with existing Rego libraries. Resources table → Rego input documents.

### L9. MITRE ATT&CK assessment (paid feature)
- Map discovered resources + edges to ATT&CK Cloud matrix techniques (T1078 Valid Accounts, T1190 Exploit Public-Facing App, T1526 Cloud Service Discovery, T1530 Data from Cloud Storage Object, T1537 Transfer Data to Cloud Account, T1580 Cloud Infrastructure Discovery, T1538 Cloud Service Dashboard, T1098.001/.003 Account Manipulation, T1199 Trusted Relationship, T1496 Resource Hijacking, etc.).
- Implemented via `disco check` — no new top-level command. Reuses rule eval + existing `Related` graph traversal (already single-hop with nestable depth).
- DSL extension: add `Tags map[string][]string` to `Rule` (free-form key→values). ATT&CK rules carry `tags: { attack.technique: [T1530], attack.tactic: [collection], attack.platform: [iaas] }`. Tag keys/values arbitrary — engine treats as opaque metadata, propagates onto `Finding`.
- `disco check --format attack-navigator` groups findings by `attack.technique` tag, emits ATT&CK Navigator JSON layer. `--format json` includes raw tags. Other tag keys (`compliance.control: [CIS-1.20]`, `owasp: [A05]`) free-ride on same mechanism.
- Graph-derived techniques (T1199 cross-account roles, T1078 lateral IAM, T1530 public-exposure) authored as rules with nested `Related` blocks — no engine changes needed.
- Cross-provider: AWS/Azure/GCP rules unified under cloud sub-matrices (IaaS, SaaS, Office 365, Azure AD, Google Workspace) via `attack.platform` tag.
- Ship as curated paid rule pack (`rules/attack/*.yaml`) gated behind license. OSS users can author their own tagged rules; navigator output formatter is the paid surface.
- Pairs with L5 (SIEM sinks) — push technique coverage to detection platforms. Pairs with L7 (Rego) — Rego rules emit same tag shape.
