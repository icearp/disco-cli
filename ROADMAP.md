# Disco Feature Roadmap

## Context

`disco` = CGO-free Go CLI scan AWS/Azure/GCP into local SQLite with resource graph (relationships + closure table). Foundation solid: parallel scan, stable resource IDs, secret scrub at store boundary, rule engine, graph query, diff.

**Primary audience:** security/compliance teams — posture review, drift detect, relationship audit.

**Strategic focus (this cut):**
1. **Coverage** — top services across all three clouds
2. **Resolvers** — same-service completeness + cross-service edges (graph useless without edges)
3. **Graph command** — power-user query surface; "blast radius", "path", "why"

Tiers: **Now (1–2 sprints)** → **Next (quarter)** → **Later (6–12mo / v1.0)**.

Shipped capabilities live in `FEATURES.md`. This file tracks only partially-implemented and unimplemented work.

---

## Focus-group follow-ups

Deferrals from an earlier review-and-remediation cycle (findings F1–F21). All of those findings shipped (see `FEATURES.md`); the items below are net-new follow-up plans, not unaddressed findings.

- **`disco export <output-file>`** — multi-format evidence bundle (snapshot archive + JSON inventory + latest-run SARIF) for one-shot auditor handoff. Composes atop `disco snapshot` + `disco list -o json` + `disco check -o sarif`. Same `.zip|.tar.gz|.tar.xz` extension-driven format detection.
- **`disco snapshot --extract` / `disco verify --extract <dst>`** — receive-side extract while verifying, for inspection without separate `tar -xf`.
- **Snapshot compression-level flags** — `--compression {fast|balanced|max}` on `disco snapshot`. xz at default level dominates wall-time on big DBs; users may want a faster output at the cost of ~2× size.
- **`azure-waf` sample pack** — 5-rule Azure Well-Architected Framework subset under `internal/policy/azure-waf/`. Same `--packs` plumbing; one rule or two per pillar. Mirror of `aws-waf`.
- **`gcp-waf` sample pack** — 5-rule GCP Well-Architected Framework subset under `internal/policy/gcp-waf/`. Same shape.
- **`--since-verified`** — `list` / `summary` / `tag-coverage` filter on `verified_at` (last-confirmed-live) instead of `discovered_at` (first-seen). Pairs with `--since`; documents the "what's still live" question that `--since` alone can't answer.

---

## NOW — 1–2 sprints

Theme: finish AWS resolver debts before sprawl to more providers.

### R1. Same-service AWS resolver gaps

Fully landed; see `FEATURES.md`.

### R2. New AWS scanners — highest graph/security value

Almost fully landed (R2.1–R2.18); see `FEATURES.md`. Outstanding:

- **R2.14 Artifact** — deferred (S3-backed compliance reports, narrow graph value).
- **R2.18 sub-resources** — deferred per landed parent: Glue crawlers/jobs/triggers/classifiers/connections; Athena named-queries/prepared-statements; Redshift parameter-groups/snapshots/Serverless; OpenSearch outbound-connections/packages.
- Other deferred-within-landed-services:
  - **SSM document** content → referenced IAM role (needs YAML/JSON doc-content parser).
  - **Backup selection** → tagged resources (needs tag-condition-expression expansion against resources table).
  - **GuardDuty detector** → member accounts via `loadOrgTargetIndex` (pattern proven in Detective / Inspector2 / SSO resolvers; FK-safe across accounts when Org tree scanned).

### R3. Azure scanner expansion

R3.1–R3.23 landed; see `FEATURES.md`. Outstanding:

- **R3.24 RBAC Entra principal wiring** — `authorization_resolvers.go` synthesizes `foreign-subscription` stubs but does not yet upgrade `roleAssignment.principalId` matches against the Entra-scanned `azure:microsoft.entra:user|group|service-principal|application` rows. Build per-tenant `principalId → resource-ID` index; emit `assignment -[uses]-> principal` edges. Closes the residual leg of R3.1.
- **R3.25 Diagnostic-settings allowlist extension** — `diagnosableTypes` covers the high-frequency types but lacks ~12 newer/secondary diagnosable types: PostgreSQL/MySQL flexible-server, ACI, Event Grid (topic/system-topic/domain), Data Factory, Logic Apps, API Management, Traffic Manager, CDN profile, SQL Managed Instance. Append to slice; no logic change.

### R4. GCP scanner expansion

R4.1–R4.22 landed; see `FEATURES.md`. (R4.14 Dataproc/Dataflow fan-out, R4.21 Cloud Identity +
Workspace Directory, and R4.22 non-SA IAM member edges were previously listed here as
outstanding — verified landed in code during the 2026-07-06 GCP coverage audit; this section
was stale.) Outstanding:

- **R4.23 Workforce + Workload Identity Pools** — `gcp:iam:workforce-pool` (org-scope, `iam.Locations.WorkforcePools.List(parent="locations/global")`) + `gcp:iam:workload-identity-pool` (project-scope, `iam.Projects.Locations.WorkloadIdentityPools.List`). Resolver: pool provider → external-IdP issuer URL (string-only until something else scans IdPs). Federation trust edges across cloud boundaries. Folded into R4.24 wave 8 below.

**R4.24 GCP type-coverage buildout** — `docs/gcp-type-coverage.md` (2026-07-06 audit) found only
56 GCP resource types scanned against ~228 real, listable, currently-unscanned types across
Compute Engine + 26 other services. Wave 1 (Compute storage domain: Disk, Image, Snapshot
family), Wave 2 (Compute instance groups & templates: InstanceGroup\*, InstanceTemplate\*,
InstanceGroupManager\* incl. nested ResizeRequest), Wave 3 (Compute addressing: Address,
GlobalAddress, \*Prefix), and Wave 4 (Compute networking core: Route, Router, VpnGateway family,
NEG family, NetworkFirewallPolicy family, NodeGroup/Template, PacketMirroring,
ServiceAttachment, NetworkEdgeSecurityService, CrossSiteNetwork/WireGroup), Wave 5 (Compute
Interconnect: Interconnect, InterconnectAttachment, InterconnectGroup,
InterconnectAttachmentGroup), and Wave 6 (Compute LB/health-check/SSL-TLS: GlobalForwardingRule,
HealthCheck family, HttpHealthCheck/HttpsHealthCheck, SslCertificate/SslPolicy families,
TargetSslProxy/TargetTcpProxy/TargetGrpcProxy, regional TargetHTTP(S)Proxy/UrlMap/BackendBucket,
BackendService dual-type split, TargetInstance, TargetPool), and Wave 7 (Compute autoscaling +
reservations: Autoscaler/RegionAutoscaler, Reservation → ReservationBlock →
ReservationSubBlock 3-level nested fan-out, FutureReservation, RegionCommitment,
ResourcePolicy, RegionSecurityPolicy — ReservationSlot, a 4th nesting level with unbounded
per-subblock cardinality and no edges of its own, deferred), Wave 8a (cloudkms:
CryptoKeyVersion, EkmConnection, ImportJob, KeyHandle, SingleTenantHsmInstance, inlined into the
existing `scanCloudKMS` nested loops), and Wave 8b (cloudresourcemanager: TagKey/TagValue/TagHold
3-level nested fan-out from both org and project entry points — a TagKey can be parented by
either; Lien/TagBinding/EffectiveTag project-scoped, the latter two deliberately not fanned out
to every scanned resource per docs/gcp-type-coverage.md), Wave 8c (accesscontextmanager:
AccessLevel/AuthorizedOrgsDesc inlined as new siblings in the existing per-AccessPolicy fan-out,
GcpUserAccessBinding parented directly by the org — `vpcsc_scanners.go` gained its first test
file this sub-wave), and Wave 8d (sqladmin: BackupRun/Database/SslCert/User fanned out per
already-scanned Instance via bounded-concurrency `forEachItem`; fixed a pre-existing
`singularize()` coverage-tool bug found while verifying `Database`'s alias — "databases" and
"aliases" share an identical `-ases` suffix with no suffix-only way to tell apart which one's
singular ends in a sibilant vs. a silent "e") landed; remaining waves tracked here, implement
independently in any order:

- **Wave 8 (remaining: 8e-8g)** — dns, cloudidentity (Device/SSO/Membership/Policy), iam (closes R4.23 + adjacent Namespace/OauthClient/custom-Role).
- **Wave 9** — Observability: logging (Bucket/Exclusion/Metric/View/LogScope), monitoring (Dashboard/NotificationChannel/Service/SLO/Snooze/UptimeCheckConfig).
- **Wave 10** — Data services secondary resources: spanner, bigtableadmin, firestore (Backup/BackupSchedule/UserCred only — Field/Indexes stay DEFER), bigquery, dataproc.
- **Wave 11** — Storage/artifact/build/misc secondary: storage, artifactregistry, cloudbuild (needs new `cloudbuild/v2` import for Connection/Repository), run (needs new `run/v1` import for legacy Domainmapping), container NodePool, certificatemanager, composer, dataflow, secretmanager, pubsub.

Full verdict ledger (INCLUDE/DEFER/DROP + client/method per type) in `docs/gcp-type-coverage.md`.

### R5. Cross-service resolvers (multi-provider aware)

AWS cross-account-trust, Azure cross-sub-rbac, GCP cross-project-iam landed (see `FEATURES.md`). Outstanding:

- **Cloud-to-cloud DNS dangling check** — deferred until L1 Cloudflare provider lands.

---

## NEXT — this quarter

### G1. Graph subcommands + filter flags

Landed; see `FEATURES.md`. Edge `source_resolver` tooltip render still pending G2 schema column.

### G2. Relationships table evolution

- Add `source_resolver TEXT` column (migration) populated from resolver name via context.
- Add `confidence REAL` column for heuristic edges (e.g. Route53 alias reverse-lookup). Spec consumer alongside the column: `disco graph --min-confidence 0.8` filter + rule-engine predicate; otherwise drop the column.

### G4. Rule engine expansion

Graph-aware rules + tag/category/remediation propagation landed; see `FEATURES.md`. Outstanding:

- **Severity / compliance profiles** — partial: filtering shipped (`disco check --tag k=v`); first-party CIS / NIST 800-53 / PCI-DSS / Well-Architected packs are future work, not yet bundled — the engine ships without bundled curated content; users load their own bundles via `--rules`.
- **Suppression** — `disco check --suppress suppressions.yaml` (by resource ID or rule ID + scope).
- **Baseline diff** — `disco check --baseline findings.json` reports only new findings since baseline.

### G5. `disco coverage`

Landed; see `FEATURES.md`.

### G7. `disco check --output sarif`

Landed; see `FEATURES.md`.

### G8. `disco export` / `disco import`

Portable DB snapshots: export resources + relationships + scans to single JSONL bundle, re-importable into fresh DB. Distinct from L5 SIEM sinks — this for offline analysis, airgapped reviews, support dumps.

### G9. Redaction verification + deprecation registry

- `disco audit --redaction` — re-runs `scrubAttributes` over stored rows, flags entries written before denylist update.
- Type-deprecation registry — `emits []coverage.TypeDecl` entries marked deprecated; list / graph annotate affected rows.

---

## LATER — 6–12mo / v1.0

### L1. New providers

- Kubernetes (kubeconfig-driven; feed pods/services/secrets/ingresses into graph; bridges EKS/AKS/GKE context).
- Oracle Cloud, DigitalOcean, Cloudflare (DNS/WAF adjacent).

### L8. Query language

- `disco query 'provider=aws type=kms:key upstream=lambda:function'` — unified over list + graph. Stretch; revisit after G1 stabilizes.

---

## Cross-cutting

- **Scan retention / compaction** — policy for pruning old `scans` + orphaned `resources` rows on long-lived DBs. Distinct from G3 (incremental); retention is time-axis concern.
- **Multi-account AWS role chaining** — codify cross-account `AssumeRole` workflow for scanning N accounts from one runner (config shape, credential cache, per-account scan record). Companion to R5 cross-account-trust edges.
- **Performance** — benchmark harness (`go test -bench`) with fake provider emitting N resources. Target: 10k resources/sec UpsertResources, 100k edges/sec for closure inserts.
- **Observability** — slog with `scan_id` correlation throughout. Redirect provider SDK logs behind flag.
- **Docs** — `disco coverage --output markdown > docs/coverage.md` regen via CI workflow (PR auto-refresh) — `disco coverage` already produces README-fit markdown; the workflow piece is the only thing missing.
- **CI** — coverage budget enforcement per-package. (Per-provider test gates dropped — CI runs `go test ./...`; a "per-provider gate" had no concrete meaning. Reintroduce only if cred-required live-scan tests are added behind a build tag.)
- **Release** — goreleaser pipeline (linux/darwin/windows amd64+arm64), signed binaries.

---

## Deferred / tracked gaps

### Lint / cleanup
- `internal/providers/aws/apigateway_resolvers.go` — three remaining `strings.Index` sites (grep `strings.Index`) → `strings.Cut` simplifications. Pre-existing.

### Synthetic NativeIDs — KMS grant
- `aws:kms:grant` NativeID synthesized as `{keyARN}/grant/{grantId}` because `ListGrants` returns no canonical ARN (only `GrantId`). Shape is stable across rescans but not an AWS-issued identifier — anything that pattern-matches "`arn:aws:kms:...`" ARNs elsewhere (e.g. future cross-service resolvers, external tools consuming `disco export`, SIEM enrichment) could collide or misclassify. If AWS ever adds a `GrantArn` field to the ListGrants response (or `DescribeGrant` surfaces one), switch to it and migrate; the synthetic form only needs to hold until then. Precedent for synthetic shape: EFS mount-target + Backup selection already use similar `{parent}/...` patterns (documented in CLAUDE.md).

### Persisting non-resource scan state
- S3 bucket encryption config (`GetBucketEncryption`) currently lives only on `account.s3BucketEncryption` during one scan+resolve run. KMS edge is persisted; underlying `ServerSideEncryptionConfiguration` is not. Not visible in `disco list` / `disco graph <bucket> --output json` attrs. If we later want the raw config queryable (rule-engine checks like "bucket encrypted with CMK of rotation age < N days", compliance exports), add a generic sidecar — options from the design discussion: (a) new `resource_configs(resource_id, config_type, payload_json)` table via migration, or (b) per-service sidecar column. Applies to any future `Get*Config` fetch whose result is not its own AWS resource (candidates: bucket versioning, bucket logging, bucket notification, bucket public-access-block, bucket replication, queue attributes not already flattened). Do not wrap the primary resource's SDK attrs — that's been ruled out.

### Not worth building (for now)
- Real-time streaming scan (SQS/EventBridge-driven). Batch fine.
- Fancy CLI TUI (`disco ui`).
- Built-in secret scanning of resource attrs beyond key-name denylist. Out-of-scope; use dedicated tools (trufflehog etc.) upstream.

### EventBridge `EventBusArn` dead resolver branch
- `eventbridge_resolvers.go:32` reads `attrs.Rule.EventBusArn` then falls back to `EventBusName`. SDK `eventstypes.Rule` has no `EventBusArn` field and `eventbridge_scanners.go` never synthesizes one — the first branch is dead in production. Two clean-up options: (a) remove the field + branch; or (b) populate it scanner-side at line 87 (synthesize from region+account+busName so resolvers and `disco list` see the canonical ARN). Picked up while migrating EB resolver-test fixtures to SDK-typed shapes (P4); functionality unaffected because the fallback covers all real scans.

### Scanner unit-test interface lift incomplete (P2)
- `sqs_scanners.go` is the only scanner refactored to take a narrow `<svc>API` interface for testability (precedent: `sqsAPI` + `scanSQSQueues`, with stub in `sqs_scanners_test.go`). 71 other scanners still take concrete `*<svc>.Client` and have no scanner-side test coverage (only resolver tests via hand-rolled DB rows). Lift incrementally as scanners change — not a wholesale sweep. AWS SDK Go v2 unit-testing guide §"Mocking client operations".

### Resolver-test SDK-typed fixture migration incomplete (P4)
- 9 resolver tests migrated to use real SDK types via `wrapped_attrs_testhelper_test.go` (5 ELBv2, 4 EventBridge). ~75 resolver tests still build `AttributesJSON` from hand-rolled JSON strings or `map[string]any`. Hand-rolled fixtures pass on `json:"PascalCase"` mismatches that real scans would silently fail on. Continue migration whenever resolver tests are touched — extend the helper file with new `<svc><Resource>Attrs` builders for each scanner-side wrapper shape.

### Azure scanner-test fake-transport pilot incomplete (P2)
- Pilot landed in `compute_disks_scanners_test.go`: `scanDisks` covered via `armcomputefake.DisksServer` + `azfake.PagerResponder`, with happy-path / multi-page pagination / 403 AccessDenied. Helpers in `fake_testhelper_test.go` (`fakeCred`, `fakeClientOptions`). Roughly **80+ Azure scanners** still untested at scanner level — every `azPageScan`, `azRGFanoutScan`, `sqlChildScan`, multi-phase `wan_scanners.go`, every `azSimpleScan` user. Pattern requires splitting `scanX(ctx, sub, cred, st, scanID)` into `scanXWithClient(ctx, sub, st, scanID, client)` body + thin wrapper — only `scanDisks` is split today, so the package is currently inconsistent. Migrate opportunistically as scanners change.
- `azRGFanoutScan` deferred specifically because faking it requires also stubbing `armresources.ResourceGroupsClient` (RG enumeration runs first); easiest target is `scanVirtualNetworkGateways` in `wan_scanners.go` once a tester wants to take the first crack.

### GCP scanner-test httptest pilot incomplete (P2)
- Pilot landed in `loadbalancing_scanners_test.go`: `scanForwardingRules` covered via `httptest.Server` + `option.WithEndpoint` + `option.WithoutAuthentication`, with happy-path / real-403 ScanWarning / `accessNotConfigured` sentinel. Helpers in `fake_testhelper_test.go`. Roughly **20+ GCP scanners** still untested at scanner level. No production-code refactor required — per-phase scanners already accept `*compute.Service` / `*iam.Service`. Endpoint-path gotcha documented in `internal/providers/gcp/CLAUDE.md`: route keys strip the `/compute/v1` prefix.
- Pub/Sub admin emulator path (`PUBSUB_EMULATOR_HOST`) flagged in plan but unwired; only worth doing if `pubsub_scanners.go` later wants integration coverage exercising the real SDK auth + retry pipeline.

### Resolver-test SDK-typed fixture migration — Azure + GCP (P4)
- Azure: only `compute_disks_resolvers_test.go` migrated to `marshalAttrs(t, armcompute.X)` via `attrs_testhelper_test.go`; ~33 other Azure resolver tests still hand-roll JSON literals. Highest-value target because `arm*` types use custom `MarshalJSON` with `populate("camelCaseKey", ...)` — JSON shape is invisible on struct tags, so string-literal drift passes silently. GCP: only `compute_resolvers_test.go` migrated; ~21 other resolver tests still use hand-rolled JSON. Discovery types carry JSON struct tags so drift risk is lower than Azure, but maintenance cost is identical. Migrate as resolver tests are touched, mirroring the AWS pattern above.

### CFN `cfnTypeMap` shape regression test deferred
- `cloudformation_resolvers.go` `cfnTypeMap` maps `AWS::Service::Resource` → `(disco type, physID-shape)`. Per `aws/CLAUDE.md` "CFN `PhysicalResourceId` shape varies per ResourceType", wrong synthesis silently FK-drops edges with no error. No regression test catches new entries with wrong shape. Build a table-driven test: per CFN type, fixture `PhysicalResourceId`, expected NativeID — ensures shape stays canonical when adding entries. Deferred from P1 quick wins as larger-than-quick.

---

## Verification

Roadmap doc. Validate by:
1. User review for priority/scope fit.
2. Each numbered item (R1 / R2.N / R3.N / R4.N / G1 / ...) → tracked issue with acceptance criteria before implementation.
3. Resolver additions: existing test pattern per `CLAUDE.md` — `newTestStore` + `upsertTestResource` + `assertRelationship`.
4. New scanner additions: update `expectedAWSServices` / equivalent Azure+GCP, add `<svc>_resolvers_test.go`, verify in `types` command output.
