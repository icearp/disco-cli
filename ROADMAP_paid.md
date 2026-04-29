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
