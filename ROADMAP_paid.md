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
