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

R4.1–R4.23 landed; see `FEATURES.md`. (R4.14 Dataproc/Dataflow fan-out, R4.21 Cloud Identity +
Workspace Directory, R4.22 non-SA IAM member edges, and R4.23 Workforce + Workload Identity
Pools — `gcp:iam:workforce-pool`/`gcp:iam:workload-identity-pool`, landed as part of R4.24 Wave
8g below — were previously listed here as outstanding; this section was stale.)

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
singular ends in a sibilant vs. a silent "e"), and Wave 8e (dns: DnsKey/Policy/ResponsePolicy/
ResponsePolicyRule added to the existing `scanCloudDNS`, reversing its own Wave-1-era deferral
note; DnsKey fans out per already-scanned zone, ResponsePolicyRule per already-listed
ResponsePolicy; `dns_scanners.go` split into thin wrapper + `scanCloudDNSWithClient` test seam
and gained its first test file this sub-wave), Wave 8f (cloudidentity: Device/DeviceUser/
ClientState/Membership/InboundOidcSsoProfile/InboundSamlSsoProfile/IdpCredential/
InboundSsoAssignment/Policy/Userinvitation added to the existing `scanCloudIdentity`;
DeviceUser/ClientState use the API's wildcard parent rather than fanning out per already-scanned
parent, deriving each row's owner by splitting its own resource name KMS-style; added a redact
rule for InboundOidcSsoProfile's input-only OAuth client secret; first test file this sub-wave),
and Wave 8g (iam: new `iam_federation_scanners.go` — WorkforcePool → Provider → ScimTenant
org-scoped, WorkloadIdentityPool → Provider / → Namespace → ManagedIdentity and OauthClient →
Credential project-scoped, custom Role both scopes; `TypeIAMProvider` is one disco type shared
by two distinct SDK structs since the Discovery API's collection name collides at both nesting
paths; closes R4.23) — **Wave 8 fully landed**. Wave 9a (logging: `scanLogging` extended from
sinks-only into a 7-phase orchestrator — Sinks, Buckets (wildcard `locations/-`), per-Bucket
Links/Views fan-out, Exclusions, Metrics, LogScopes (literal `locations/global` — SDK doc:
global-only, not a wildcard), SavedQueries (wildcard); Metric NativeID uses the SDK's
already-URL-encoded `ResourceName` field rather than hand-building the path, since
`LogMetric.Name` may itself contain `/`) and Wave 9b (monitoring: `scanMonitoring` extended from
AlertPolicies-only into a 7-phase orchestrator — AlertPolicies, Dashboards (`monitoring/v1`,
separate API version), Groups (Members embedded via `embedMembersJSON` rather than given an
independent type, since `MonitoredResource` carries no name/ID of its own), NotificationChannels
(`labels.*` redacted), Services → SLOs fan-out, Snoozes, UptimeCheckConfigs; also fixed a
`singularize()` coverage-key bug ("snoozes" → "Snooz" instead of "Snooze", same sibilant-stem
ambiguity class as the existing "databases" exception) — **Wave 9 fully landed**. Wave 10a
(spanner: `scanSpanner` extended from Instances/Databases-only into a 7-phase orchestrator —
InstanceConfigs (Google-managed catalog entries flagged `ManagedByProvider`, review-caught gap),
InstancePartitions (wildcard `instances/-`, per-row parent derived via `strings.Cut` since one
page can mix partitions from multiple instances), Databases (fixed `.Do()` → `.Pages()`,
review-caught — a truncated first page would silently starve the new BackupSchedule/DatabaseRole
fan-outs that consume its output), Backups fan-out per Instance, BackupSchedules/DatabaseRoles
fan-out per Database) — landed. Wave 10b (bigtableadmin: `scanBigtable` extended from
Instances/Clusters-only into a 10-phase orchestrator — AppProfiles (project-wide wildcard
`instances/-`), Tables/LogicalViews/MaterializedViews fan-out per Instance, AuthorizedViews/
SchemaBundles fan-out per Table, Backups/MemoryLayers fan-out per Instance using the cluster
wildcard `clusters/-`, HotTablets fan-out per Cluster — the one endpoint individually confirmed
to lack wildcard support rather than assumed from siblings; adversarial review found zero real
issues) — landed. Wave 10c (firestore: `scanFirestore` extended from Databases-only into a
4-phase orchestrator — Backups (project-wide wildcard `locations/-`, parented via the SDK's own
`Backup.Database` field rather than name-splitting), BackupSchedules/UserCreds fan-out per
Database; none of the three new endpoints paginates at all, verified individually; UserCreds
gets a defensive `securePassword` redact rule; adversarial review found zero real issues) —
landed. Wave 10d (bigquery: `scanBigQuery` extended from Datasets/Tables-only with Models/
Routines fan-out per Dataset and RowAccessPolicies fan-out per Table, nested inside the Tables
page loop so the parent Table row is always committed first; NativeIDs synthesized from each
type's own `*Reference` struct since — unlike Dataset/Table — none carries an SDK-issued opaque
`.Id`; `scanBigQuery` split into a thin wrapper + `scanBigQueryWithClient` test seam since this
service had no test file before this wave; adversarial review caught a real bug — the
RowAccessPolicies per-table branch could escalate an `isAPINotEnabled`-shaped error to the
whole-service disabled sentinel despite Datasets.List already proving the API enabled, fixed to
always warn-and-continue for that nested call, fail-first verified) — landed. Wave 10e (dataproc:
`scanDataproc` extended from Clusters-only into a 7-phase per-region orchestrator —
AutoscalingPolicy/WorkflowTemplate/Job parented `projects/{p}/regions/{region}`,
Batch/Session/SessionTemplate parented `projects/{p}/locations/{region}`, each verified against
its own SDK URL template rather than assumed from siblings; Job has no SDK `Name` field, NativeID
synthesized from `JobReference.JobId`, List call takes positional `(projectId, region)` args
unlike the other 6 endpoints' single-parent-string form; introduced a 3-layer test seam
(`scanDataproc` → `scanDataprocWithClient` → `scanDataprocIn`) that also fixed a latent
inefficiency — regions now resolve once per scan and thread via `gcpRegionFanoutScanIn` into all 7
phases instead of each phase independently re-resolving them; adversarial review found zero real
issues) — **Wave 10 fully landed**. Wave 11a (storage: `scanStorage` extended from Buckets-only
into a 3-phase orchestrator — HmacKeys per-project, then per-bucket fan-out for Notifications/
ManagedFolders/AnywhereCaches/Folders/BucketAccessControls/DefaultObjectAccessControls; the 3
opt-in-feature endpoints (ManagedFolders/AnywhereCaches/Folders) tolerate a bare 400 via a narrow
`isBucketFeatureNotApplicable` predicate so one bucket lacking hierarchical-namespace/cache
doesn't abort the scan; `HmacKeyMetadata`'s list shape carries no secret so the audit's
"flag for redaction" note doesn't apply; fixed a `singularize()` bug — `"anywhereCaches"` was
mis-reduced to `"anywhereCach"` by the sibilant-stem rule, added to `singularizeExceptions`;
adversarial review found zero real issues) — landed. Wave 11b (artifactregistry:
`scanArtifactRegistry` extended from Repositories-only into a 3-phase orchestrator —
Packages/Rules/Attachments fan-out per Repository, Tags fan-out per Package (nested two levels
deep); re-scoped the original audit's type list after reading the vendored SDK directly — Version
and the 4 format-specific per-artifact views (DockerImage/MavenArtifact/NpmPackage/PythonPackage)
share the same unbounded per-push cardinality and are deliberately not scanned, PrewarmedArtifact
turned out to have no List RPC at all; adversarial review caught the scope-decision comment
overstating its own evidence (claimed the 4 format-specific views' fields are "literally mirrored"
into Version — true for only a few DockerImage fields, false for the other 3), fixed to state the
real cardinality rationale; the 4 nested per-repo/per-package fan-out phases all apply the Wave
10d fix pattern so a nested isAPINotEnabled-shaped error can't escalate past a phase-1 call that
already proved the API enabled) — landed. Wave 11c (cloudbuild: `scanCloudBuildTriggers` extended
from Triggers-only into a 5-phase orchestrator — the first scanner to import two API-version
packages for one service (`cloudbuild/v1` + `cloudbuild/v2`, since 2nd-gen repository Connections
only exist in v2); WorkerPools (v1) and Connections (v2) fan-out per location using a v2
Locations.List catalog reused for both (v1 has no Locations.List of its own — disclosed
assumption that v1/v2 share one regional footprint), Repositories 2nd-gen (v2) fan-out per
Connection, GithubEnterpriseConfig (v1) queried at both the global and per-location parent since
it's a location-partitioned resource; GitLabConfig/BitbucketServerConfig/Repo deliberately not
scanned — self-marked experimental APIs superseded by 2nd-gen Connections, an honest usage/risk
tradeoff rather than a duplicate-data claim; adversarial review caught two real bugs — the
original code missed location-scoped GitHub Enterprise configs (global-parent-only query), and
both the Locations.List and GithubEnterpriseConfigs calls could escalate an isAPINotEnabled-shaped
error to the whole-service disabled sentinel despite phase 1 already proving the service enabled
— both fixed and fail-first verified) — landed. Wave 11d (run: `scanCloudRun` extended from
Services-only into a 6-phase orchestrator — per-Service fan-out (Revisions), per-region fan-out
(WorkerPool + Instance, reusing the Compute-regions `gcpRegions` helper since run/v2 exposes no
Locations catalog of its own), two project-scoped legacy `run/v1` calls (DomainMapping,
AuthorizedDomain — new API-version import, mirroring the 11c cloudbuild precedent);
`scanCloudRunJobs` (jobs_scanners.go) gained a per-Job Executions fan-out phase; `Instance` was a
scope addition found live during SDK research, not in the original wave-order note — a real,
independently-creatable Cloud Run resource distinct from Services/Jobs. `Domainmapping`/
`Authorizeddomain` mirror Discovery's genuinely single-word lowercase collection names; `WorkerPool`
keeps its dash (Discovery's `workerPools` is camelCase, same as the already-scanned cloudbuild
WorkerPool). Adversarial review caught two real bugs: (1) the AuthorizedDomain phase routed through
`runPaginated` (which already classifies isPermissionDenied/isAPINotEnabled internally) and then
re-classified the already-sentinel-wrapped error a second time, so its isAPINotEnabled shape fell
through to the escalate branch instead of discard — unlike every other nested phase in this wave,
which correctly used a manual `.Pages()` + single classification; fixed to match; (2) the original
`WorkerPool` alias collapsed to the single-word `Workerpool` casing to satisfy the self-consistency
type-mirror test, without checking the live Discovery doc — the test only proves the alias matches
`AlgorithmicKey`, not that either matches the real upstream key; fixed by checking
`run.googleapis.com/$discovery/rest` directly (confirms `workerPools`, camelCase) and reverting the
type to `gcp:run:worker-pool` / alias `WorkerPool`. Both fixes fail-first verified. Wave 11e
(container: `scanGKE` gained a per-Cluster NodePool fan-out; certificatemanager: 4 new phases
— CertificateIssuanceConfigs, TrustConfigs, plus the pre-existing CertificateMaps/DNSAuthorizations
retrofitted; composer: per-Environment UserWorkloadsConfigMap fan-out; dataflow: per-region Snapshot
fan-out via the same `gcpRegions`-threaded test-seam shape as 11d's run WorkerPool/Instance;
secretmanager: per-Secret Version fan-out, metadata only per SDK docs — supersedes a stale
"intentionally not scanned" comment; pubsub: Snapshot phase added, Subscriptions/Schemas
retrofitted) — landed, closing Wave 11. certificatemanager's own test suite caught a real bug
before review: its pre-existing flat sibling phases (CertificateMaps, DNSAuthorizations, and by
extension the new CertificateIssuanceConfigs/TrustConfigs) were routed through the `runPaginated`
helper, which internally classifies isPermissionDenied/isAPINotEnabled and returns an
already-`errServiceDisabled`-wrapped sentinel on API-not-enabled — re-checking isPermissionDenied on
that result a second time can never match, since `markServiceDisabled` embeds the original error via
`%s` not `%w`. This generalizes the discard-not-escalate rule beyond genuine per-item fan-outs
(Wave 10d/11b-d) to ANY phase after the first in a scanner, including flat siblings of the same API —
all of certificatemanager's phases 2-5 were retrofitted to a manual `.Pages()` + single
classification, and the same lesson was applied proactively to pubsub before it could hit the same
bug. `TypeSecretVersion`'s disco type is `gcp:secretmanager:version`, not `secret-version` — the
live Discovery key is the bare word `Version`; the SDK's Go type name `SecretVersion` is a
disambiguating alias only, the same self-consistency-test trap as 11d's WorkerPool. All 6 files'
discard-pattern fixes fail-first verified before commit.

Full verdict ledger (INCLUDE/DEFER/DROP + client/method per type) in `docs/gcp-type-coverage.md`.

**Resolver buildout (follow-on to R4.24).** Waves 1-11 above shipped scanners only —
every commit deferred resolver work. A resolver-edge-coverage audit tool was retrofitted onto GCP
(mirrors AWS's `EdgeDecl`/`registerResolver(fn, emits...)` pattern — see
`internal/providers/gcp/gcp_registry.go`, `disco coverage resolvers --providers gcp`), all 22
pre-existing GCP resolvers annotated with the edges they actually emit (adversarially verified
against each resolver body, zero mismatches). This surfaced 216 of 247 emitted types with no
outbound edge. A classification pass (89 LEAF: genuinely terminal, e.g. KeyRing/Organization/
Project self-nodes, credential/identity rows already covered by inbound edges, free-text-only
config bodies; 127 GAP: real unread FK-shaped fields — KMS key refs, network self-links,
service-account emails, cross-resource name refs) flagged the 89 leaves via `Leaf: true` on their
`coverage.TypeDecl`, mirroring AWS's convention, guarded by a new `TestLeafTypesNotResolverSources`
(fail-first verified). Adversarial review of the classification caught 5 real misclassifications
(cloudkms CryptoKey→backend ref, BigQuery Table CMEK, Logging Metric→bucket ref, Monitoring
Dashboard→alert-policy ref, CRM Folder→management-project ref) — moved back to GAP. Net: 132 real
orphan types remain, tracked as the resolver-implementation backlog for future waves (not yet
started — this pass built the ledger, not the resolvers).

**Resolver Wave R1 (compute storage lineage).** First implementation wave against the 132-type
backlog: `internal/providers/gcp/compute_storage_resolvers.go` wires lineage edges for the Wave 1
disk-family types (Disk, RegionDisk, Image, MachineImage, Snapshot, RegionSnapshot,
InstantSnapshot, RegionInstantSnapshot) — every "source\*" self-link field (sourceDisk,
sourceImage, sourceSnapshot, sourceInstantSnapshot, sourceInstance) plus CMEK
(`*EncryptionKey.kmsKeyName`) read straight off the already-scanned AttributesJSON, no extra API
calls. Also resolves (Region)InstantSnapshotGroup → ResourcePolicy via `sourceConsistencyGroup`
(adversarial review caught this — the two group types were initially mis-triaged as `Leaf: true`
during Wave 0.5 on the assumption ResourcePolicy is unscanned; it is (`compute_reservation_scanners.go`),
so the flag was dropped and the edge wired instead). New shared helpers: `loadKMSCryptoKeyIndex`
(extracted from `kms_resolvers.go`, behavior-preserving) and `scannedIDSet`, both reusable by
future waves. Net: 132 → 122 orphan types. Review also corrected stale comments claiming a
relationships-table FK enforces target existence — migration `006_resource_versioning.sql`
dropped that FK when the table moved to root-ID addressing; the scanned-ID pre-check is a
data-quality guard, not an FK-violation avoidance.

**Resolver Wave R2 part 1 (instance groups + templates).**
`internal/providers/gcp/compute_instance_group_resolvers.go`: InstanceGroup/RegionInstanceGroup
→ Network/Subnetwork (its own `network`/`subnetwork` self-link fields); InstanceTemplate/
RegionInstanceTemplate → Network/Subnetwork (via `properties.networkInterfaces[]`), → Image/
Snapshot (via `properties.disks[].initializeParams.source{Image,Snapshot}`), → ResourcePolicy and
→ IAM ServiceAccount (via `properties.resourcePolicies[]` / `properties.serviceAccounts[].email`).
Adversarial review caught a real bug before merge: `InstanceProperties.ResourcePolicies` is
documented as "(names, not URLs)" — the only self-link-shaped field on the struct that isn't
actually a self-link — so the initial implementation's self-link-based `ResourceID` lookup silently
matched zero rows in practice; fixed via a name-keyed index (`buildResourcePolicyNameIndex`,
mirrors the existing `buildSAEmailIndex` shape) instead of the usual `scannedIDSet` self-link path.
New shared helper `upsertIfScanned` (self-link → ResourceID → existence-check → upsert) also
absorbed Wave R1's `upsertComputeStorageLineageEdge` internals, removing the duplication. Net:
122 → 118 orphan types.

**Resolver Wave R3 (addressing + core L3 networking).**
`internal/providers/gcp/compute_networking_core_resolvers.go`: Address/GlobalAddress →
Network/Subnetwork; Router → Network; Route → Network + next-hop (Instance, VpnTunnel,
InterconnectAttachment, ForwardingRule via `nextHopIlb`). `nextHopGateway` deliberately excluded —
confirmed via the SDK's own doc comment that its value is always the literal internet-gateway
pseudo-resource path, never a real scannable type; `nextHopPeering`/`nextHopHub` also out of scope
(bare name / no scanner respectively) — clean review, no bugs found. Net: 118 → 114 orphan types.

**Resolver Wave R4 (load balancing).** Extended the pre-existing `loadbalancing_resolvers.go`
traffic-flow chain (forwardingRule → target proxy → urlMap → backendService/backendBucket) to
cover every global/regional variant and proxy family: GlobalForwardingRule, regional target
HTTP(S) proxies, target gRPC proxy (routes via `urlMap` like HTTP(S)), target SSL/TCP + regional
TCP proxies (route via `service` directly to BackendService, no urlMap hop), regional URL map.
New `internal/providers/gcp/compute_lb_misc_resolvers.go` covers the surfaces outside that chain:
BackendBucket/RegionBackendBucket → Storage bucket (`bucketName` is a bare name, not a self-link —
matched via a name index built off `store.Resource.Name`, not `ResourceID`), TargetPool →
Instance/HealthCheck/backup-TargetPool, TargetInstance → Instance/Network. Adversarial review
caught two real bugs before merge: (1) `TargetPool.HealthChecks` only ever holds legacy
`HttpHealthCheck` self-links per the SDK's own doc comment ("Only legacy HttpHealthChecks are
supported") — the initial code resolved against the modern `HealthCheck` type instead, a dead
edge in every real project, masked by a test that fabricated the wrong SDK struct at the wrong
self-link shape; (2) the traffic-chain's own doc comment claimed forwarding rules route to
gRPC/SSL/TCP target proxies, but the `idByNative` existence index never actually included those
types, so real SSL Proxy LB / TCP Proxy LB / gRPC forwarding rules silently produced no edge —
both fixed, fail-first verified, with new tests covering the previously-untested paths. Net:
114 → 102 orphan types.

**Resolver Wave R5 (VPN + Interconnect).** New
`internal/providers/gcp/compute_vpn_interconnect_resolvers.go`: (Target)VpnGateway → Network;
VpnTunnel → VpnGateway/TargetVpnGateway/Router (`vpnTunnel.peerGcpGateway` HA-VPN-to-HA-VPN
peering deliberately deferred — a genuine second edge target, rare enough to defer past this
wave); InterconnectAttachment → Interconnect + Router; InterconnectAttachmentGroup → member
InterconnectAttachments (nested `map[string]{Attachment}`); InterconnectGroup → member
Interconnects (nested `map[string]{Interconnect}`); WireGroup → Interconnect (via nested
`Wires[].Endpoints[].Interconnect`); NetworkEdgeSecurityService → SecurityPolicy. Interconnect
itself flagged `Leaf: true` — every field describes the physical circuit or is an inbound
reference from its own attachments/groups; `Location`/`EffectiveLocation`/`RemoteLocation` are
genuine outbound refs but disco doesn't scan `InterconnectLocation`/`InterconnectRemoteLocation`,
so no resolver-eligible target exists today (Leaf reflects current scan scope, not schema shape).
Adversarial review found the scanner-side doc comment in `compute_interconnect_scanners.go` was
stale — it claimed "no resolver this wave," directly contradicted by this wave's own resolver;
corrected. Review also flagged thin test coverage on the nested map/slice traversals (only ever
tested with one member/wire); added multi-member `AttachmentGroupToMembers` and two-wire
`WireGroup` cases plus a full unscanned-targets-skipped negative test for
`resolveInterconnectRelationships` (previously only the VPN resolver had one), fail-first
verified against a corrupted single-iteration loop. Net: 102 → 93 orphan types.

**Resolver Wave R6 (network edge closeout, part 1).** New
`internal/providers/gcp/compute_network_edge_resolvers.go`: Network → NetworkFirewallPolicy
(`firewallPolicy`) + peer Network (`peerings[].network`); (Region)NetworkFirewallPolicy → Network
(`associations[].attachmentTarget` — safe because these disco types are only ever populated by
`scanComputeNetworkFirewallPolicies`, never the org-scoped `FirewallPolicies` service, so the
generic "attachment target" field can only be a network here); NetworkAttachment → Network +
Subnetwork[]; ServiceAttachment → ForwardingRule (`producerForwardingRule` — always the regional
type since `serviceAttachments` has no global variant); RegionCommitment → Reservation[]
(`existingReservations[]` — the one field in this wave with no SDK doc comment at all; corroborated
only by the sibling `Reservations` field's doc text, not authoritative, flagged for a live-scan
spot-check); NodeGroup → NodeTemplate. Scoped deliberately to only the unambiguous single-target-
type fields in the remaining "compute" bucket — deferred BackendService/RegionBackendService
(HealthChecks mixes modern/legacy health-check types), Backend.Group (five possible group/NEG
target types), Autoscaler.Target, and the HealthCheckService/HealthSource/CompositeHealthCheck
cross-referencing chain to a later wave rather than guess wrong, matching this backlog's established
"defer ambiguous, don't guess" precedent (nextHopGateway, ResourcePolicies bare names). Adversarial
review: clean, no bugs — confirmed every field/self-link claim against the vendored SDK source and
Google's live discovery doc; one pre-existing, not-new limitation noted (cross-project VPC peering
targets are structurally unresolvable since `scannedIDSet`/`upsertIfScanned` scope lookups to the
same project, same as the existing cross-project service-account-ref limitation). Net: 93 → 86
orphan types.

**Resolver Wave R7 (backend-service ambiguous fields).** New
`internal/providers/gcp/compute_backend_service_resolvers.go` picks up the two fields deferred out
of Wave R6 as genuinely ambiguous. New shared helper `upsertIfScannedAny` (added to
`compute_instance_group_resolvers.go` next to `upsertIfScanned`) tries a list of candidate disco
types in order and upserts against the first whose computed `ResourceID` is in the scanned set —
safe because GCP self-link URLs embed the resource-collection segment, so the same literal
self-link string is structurally never valid for two different candidate types.
BackendService/RegionBackendService → Network (own field); → HealthCheck via `healthChecks[]`
(candidates `{HealthCheck, HttpHealthCheck, HttpsHealthCheck}` for global rows, `{RegionHealthCheck}`
only for regional — GCP requires a regional backend service's health checks to themselves be
regional and co-located, confirmed via GCP docs); → each backend's group/NEG via `backends[].group`
(candidates spanning InstanceGroup/RegionInstanceGroup/NetworkEndpointGroup family, 5 types).
Also (Region)Autoscaler → (Region)InstanceGroupManager via `target`, dispatched by the autoscaler's
own zonal/regional scope rather than a candidate search (unambiguous). Extended the pre-existing
`cloudarmor_resolvers.go`'s BackendService→SecurityPolicy resolver to also cover
RegionBackendService (same struct, same fields, gap found during this wave's research — one-line
fix). Adversarial review: clean, no bugs — independently verified every field/self-link/legacy-
health-check claim against the vendored SDK and GCP docs, confirmed `upsertIfScannedAny`'s
type-in-hash + self-link-structure reasoning holds, confirmed all 16 EdgeDecls in the long
`resolveBackendServiceRelationships` registration match the body exactly. One test-quality gap
flagged and fixed: no test proved `upsertIfScannedAny` picks the correct candidate when multiple
different-typed decoys are present — added
`TestResolveBackendServiceRelationships_AmbiguousGroupPicksCorrectCandidate`, fail-first verified
against a truncated candidate list. Net: 86 → 83 orphan types.

**Resolver Wave R8 (Cloud KMS).** First wave to leave the "compute" domain. New
`internal/providers/gcp/kms_backend_resolvers.go`: CryptoKey → its primary CryptoKeyVersion
(`primary.name`) and → the external backend hosting its key material (`cryptoKeyBackend`, tried
against `{EkmConnection, SingleTenantHsmInstance}` via `upsertIfScannedAny` since the two are
mutually exclusive per protection level); KeyHandle → the CryptoKey it provisioned (`kmsKey`);
CryptoKeyVersion → the ImportJob used in its most recent import (`importJob`, when key material was
imported rather than generated in place). Adversarial review caught one real bug before merge: the
initial pass flagged `TypeKMSCryptoKeyVersion` as `Leaf: true` on the reasoning that its only
non-scalar field (`ExternalProtectionLevelOptions`) points outside GCP — missing that
`CryptoKeyVersion.ImportJob` is itself a real, unwired same-project resource reference; fixed by
dropping the Leaf flag and adding `resolveCryptoKeyVersionRelationships`, fail-first verified.
Review also flagged (documented, not fixed — false-negative risk only, not a wrong-edge bug) that
`CryptoKeyBackend`'s own SDK doc says its EkmConnection/SingleTenantHsmInstance enumeration is
"non-exhaustive and may apply to additional ProtectionLevels in the future." Net: 83 → 80 orphan
types.

**Resolver Wave R9 (Cloud SQL).** New `internal/providers/gcp/sql_resolvers.go`: DatabaseInstance →
Network (private VPC attachment), → CryptoKey (CMEK, reusing the existing
`loadKMSCryptoKeyIndex`/`stripCryptoKeyVersion` helpers), → IAM ServiceAccount (reusing
`buildSAEmailIndex`), → its replication primary instance (`masterInstanceName`, a bare instance
name resolved via a new `sqlInstanceNameIndex` keyed on `lastSegment(NativeID)` — same bare-name
pattern as Wave R2's ResourcePolicy fix); BackupRun → CryptoKey (CMEK); User → IAM ServiceAccount
(`iamEmail`, IAM database authentication users only). Adversarial review caught a real bug before
merge: `settings.ipConfiguration.privateNetwork` looked self-link-shaped but the sqladmin API's own
doc example (confirmed via Google's live API reference) gives a *relative* resource link
(`/projects/p/global/networks/default`), never the fully-qualified
`https://www.googleapis.com/compute/v1/...` format Compute's own `Network.SelfLink` uses as its
NativeID — an exact-string `ResourceID` lookup across those two API families would never match on
real data. Fixed with a new `networkNameIndex` (bare-name lookup, same shape as
`sqlInstanceNameIndex`); the happy-path test was also corrected to use each field's *real* format on
each side (relative link vs. fully-qualified self-link) rather than the same abbreviated style on
both, since the original fixture had accidentally masked the mismatch — fail-first verified against
an exact-string-match regression. Net: 80 → 77 orphan types.

**Resolver Wave R10 (Cloud Storage ACL/notification children).** New
`internal/providers/gcp/storage_acl_resolvers.go`: HmacKey → IAM ServiceAccount
(`serviceAccountEmail`, reusing `buildSAEmailIndex`); Notification → Pub/Sub Topic (`topic`,
formatted `//pubsub.googleapis.com/projects/{p}/topics/{name}` per the vendored `storage-gen.go`
doc comment — independently re-verified against the raw source this wave, given the exact-format
bug class Waves R8/R9 both hit; the stripped prefix matches `TypePubSubTopic`'s NativeID verbatim,
no bare-name fallback needed here); BucketAccessControl + DefaultObjectAccessControl → IAM
ServiceAccount (`email`, one resolver covering both types since they share the identical field/
shape — empty for allUsers/allAuthenticatedUsers/project-team entities, no special-casing
required). Adversarial review found no bugs — first clean wave since R6/R7. Confirmed
`TypeStorageManagedFolder`/`TypeStorageAnywhereCache`/`TypeStorageFolder`'s existing `Leaf: true`
flags remain correct (no cross-resource reference fields on any of the three). Net: 77 → 73 orphan
types.

**Resolver Wave R11 (Cloud Monitoring).** New `internal/providers/gcp/monitoring_resolvers.go`: 5
resolvers, all within the monitoring/v3 (+v1 Dashboard) API family. AlertPolicy →
NotificationChannel (`notificationChannels[]`), Snooze → AlertPolicy (`criteria.policies[]`),
Group → parent Group (`parentName`, self-referential) — all three are full
`projects/{p}/xxx/{id}` resource-name strings matching each target's own `.Name`-as-NativeID
convention, so plain `scannedIDSet`/`upsertIfScanned` exact-match applies (same-API-family fields
reliably match per this backlog's rule). UptimeCheckConfig → Group (`resourceGroup.groupId`) is
the one exception: the SDK doc comment explicitly calls out `groupId` as the bare `[GROUP_ID]`
only, never the full path — handled with a new `monitoringGroupNameIndex` bare-name lookup;
fail-first verified against a regression to exact-match. Adversarial review caught a real bug
before merge: `TypeMonitoringDashboard` had been given `Leaf: true` on the (wrong) assumption that
a dashboard body is pure chart-layout config with no outbound refs — `Dashboard.Annotations.{
DefaultResourceNames, EventAnnotations[].ResourceNames}` are real project-name references
(`projects/{id}`) scoping which project's logs an event annotation searches. Fixed by dropping
the flag and adding `resolveMonitoringDashboardRelationships` (Dashboard → Project), reusing the
cross-project insert-if-absent placeholder pattern from `resolveIAMPolicyRelationships` for
projects outside the current scan. Net: 73 → 68 orphan types.

**Resolver Wave R12 (Dataproc).** New `internal/providers/gcp/dataproc_resolvers.go`: Cluster →
Network/Subnetwork (`gceClusterConfig.{networkUri,subnetworkUri}` — the SDK doc explicitly allows
"a full URL, partial URI, or short name", all three collapsing to the same trailing segment, so
resolved via bare-name index rather than exact match), → IAM ServiceAccount
(`gceClusterConfig.serviceAccount`, plain email), → CryptoKey (`encryptionConfig.{
gcePdKmsKeyName,kmsKey}` — genuinely a full same-cloudkms-family resource name, unlike the network
fields, so exact match via the existing `loadKMSCryptoKeyIndex`), → Storage Bucket
(`config.configBucket` — documented as a bare bucket name, not a `gs://` URI or self-link, so
bare-name index against Bucket's self-link-derived NativeID); Job → Cluster
(`placement.clusterName`, a bare name reconstructed into the composite Cluster NativeID using the
Job row's own Region, since both are region-scoped by the same fan-out — fail-first verified this
region-scoping is load-bearing, not incidental, via a wrong-region test). Extracted the
now-3x-repeated bare-name-index body (previously hand-rolled separately in Wave R9's
`networkNameIndex`/`sqlInstanceNameIndex` and Wave R11's `monitoringGroupNameIndex`) into a shared
`bareNameIndex(p, st, rtype)` helper in `compute_storage_resolvers.go`; all three now delegate to
it, behavior-preserving (confirmed by the existing R9/R11 tests staying green unchanged).
Adversarial review found no bugs. Net: 68 → 66 orphan types.

**Resolver Wave R13 (Cloud Spanner).** New `internal/providers/gcp/spanner_resolvers.go`: Instance →
InstanceConfig (`config`), InstanceConfig → InstanceConfig (`baseConfig`, self-referential —
user-managed config to its Google-managed base), InstancePartition → InstanceConfig (`config`,
same field/format as Instance's), Backup → Database (`database`) and → CryptoKey
(`encryptionInfo.kmsKeyVersion` / `encryptionInformation[].kmsKeyVersion`, via the existing
`loadKMSCryptoKeyIndex`/`stripCryptoKeyVersion`). All exact-match — confirmed via `go doc` that
none of this wave's fields carry Dataproc's "full URL, partial URI, or short name" ambiguity;
every field is documented as exactly one full resource-name format matching its target's own
NativeID. Parent-child containment (Database/Backup→Instance, BackupSchedule/DatabaseRole→
Database, InstancePartition→Instance) was already covered by the scanner's
`RecordHierarchyBatch`/`upsertWithParent` closures, so these resolvers only add the edges the
hierarchy walk doesn't produce. Adversarial review caught a real duplicate: the wave's initial
`resolveSpannerDatabaseRelationships` (Database → CryptoKey via `encryptionConfig.kmsKeyName`)
re-implemented an edge `resolveDatabasesRelationships` (`databases_resolvers.go`, pre-existing)
already owned — that resolver already covers Bigtable/Firestore/Spanner CMEK in one place. Fixed
by dropping the duplicate resolver and instead extending the existing one to also cover the
multi-region `kmsKeyNames[]` array form (previously only the singular `kmsKeyName` was handled),
closing a small real gap rather than adding a second competing resolver for the same edge — this
is also why the pre-fix orphan count only dropped by 4 of the wave's 5 resolvers (Database was
never actually an orphan). Net: 66 → 62 orphan types.

**Resolver Wave R14 (Cloud Run children).** Extended the pre-existing `serverless_resolvers.go`
(not a new file — it already owned `TypeCloudRunSvc`'s SA edge) with a second resolver,
`resolveCloudRunChildRelationships`: Revision → IAM ServiceAccount + CryptoKey (flat
`serviceAccount`/`encryptionKey` fields), WorkerPool → same two targets but nested under
`template.*` (confirmed via `go doc` — WorkerPool's fields live one level deeper than
Revision/Instance's), Instance → same two targets (flat, same shape as Revision), DomainMapping →
Service via `spec.routeName` — a bare Knative route name (per `run/v1.DomainMappingSpec`'s own doc
comment), not `TypeCloudRunSvc`'s full run/v2 resource name, so resolved via the existing
`bareNameIndex` helper rather than exact match. Grepped for pre-existing `EdgeDecl` registrations
on all 4 types before writing anything (the R13 duplicate-resolver lesson) — confirmed zero prior
coverage. VpcAccess.Connector (present on all three per-revision-shaped types) stays deferred,
matching the pre-existing Service-level deferral note (`vpcaccess.googleapis.com` scanner not yet
landed). Adversarial review found no bugs. Net: 62 → 58 orphan types.

**Resolver Wave R15 (Cloud Bigtable secondary resources).** New
`internal/providers/gcp/bigtable_resolvers.go`: AppProfile → Cluster (via
`singleClusterRouting.clusterId` / `multiClusterRoutingUseAny.clusterIds[]`), Backup → Table
(`sourceTable`) and → Backup (`sourceBackup`, self-referential copy lineage), Table → Backup
(`restoreInfo.backupInfo.backup`, restore lineage), HotTablet → Table (`tableName`, `attached-to`
kind — a diagnostic child, not a runtime dependency). Grepped for pre-existing `EdgeDecl`
registrations first per the R13 lesson — confirmed `databases_resolvers.go` only touches
`TypeBigtableCluster`'s CMEK, no overlap. Adversarial review caught a real bug: the initial
AppProfile resolver used the shared `bareNameIndex` helper against `TypeBigtableCluster`, but
Bigtable cluster IDs are only unique **within their owning instance** (SDK doc: "the ID to be
used when referring to the new cluster within its instance"), not project-wide like
`bareNameIndex`'s other consumers (networks, SQL instances) — two instances in the same project
can both have a cluster named `c1`, and the bare-name map would silently collapse them to
whichever one sorted last, producing a wrong edge rather than an error. Fixed by adding a new
`nativeIDIndex(p, st, rtype)` helper (full-NativeID keyed, alongside `bareNameIndex` in
`compute_storage_resolvers.go`) and reconstructing the instance-scoped full cluster name from the
AppProfile's own NativeID + the bare cluster ID, mirroring the Wave R12 Dataproc
region-reconstruction precedent. A dedicated cross-instance-collision test proves the fix
(fail-first confirmed red against the bare-name-index version). Net: 58 → 54 orphan types.

**Resolver Wave R16 (Cloud Logging secondary resources).** Extended the pre-existing
`observability_resolvers.go` (already owned `TypeLoggingSink`'s destination edges) with four new
resolvers: LogBucket → CryptoKey (`cmekSettings.kmsKeyName`), Link → BigQuery dataset
(`bigqueryDataset.datasetId`, in the `bigquery.googleapis.com/projects/{p}/datasets/{ds}` shape —
extracted the existing sink resolver's inline path-conversion logic into a shared
`bqDatasetIDFromResourcePath` helper and reused it for both), LogScope → Project or → LogView
(classified per-entry in `resourceNames[]`: `/views/`-containing entries exact-match `TypeLoggingView`,
everything else is a bare `projects/{id}` reference resolved via the cross-project placeholder
pattern — GCP project IDs can't contain `/`, so the discriminator is unambiguous), LogMetric →
LogBucket (`bucketName`, empty for project-scoped/non-Bucket metrics). Grepped for pre-existing
`EdgeDecl` registrations on all 4 types first per the R13 lesson — confirmed zero prior coverage.
Adversarial review found no bugs — every field/format claim checked out against `go doc`. Net:
54 → 50 orphan types.

**Resolver Wave R17 (Cloud DNS network bindings).** Extended the pre-existing `dns_resolvers.go`
(already owned the record-set → forwarding-rule edge) with three new resolvers: ManagedZone →
Network (`privateVisibilityConfig.networks[].networkUrl`, kind `attached-to` — the zone is
visible from these VPCs; and `peeringConfig.targetNetwork.networkUrl`, kind `uses` — the zone
forwards resolution to that VPC, a distinct edge kind for a distinct field on the same type pair),
Policy → Network and ResponsePolicy → Network (both `networks[].networkUrl`, kind `attached-to`).
All four `networkUrl` fields are documented as the exact same Compute self-link URL shape
Compute Network's own NativeID already uses (`net.SelfLink`), confirmed via `go doc` against
`compute_scanners.go`'s `scanComputeNetworks` — no bare-name/self-link mismatch (Wave R9's usual
trap), so exact match via the existing `nativeIDIndex` helper (from Wave R15) sufficed; added a
one-line `networkIDByNetworkURL` wrapper for readability at each call site. Grepped for pre-existing
`EdgeDecl` registrations on all 3 types first per the R13 lesson — confirmed zero prior coverage.
Adversarial review found no bugs. Net: 50 → 47 orphan types.

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
