# Disco Features

Shipped, in-tree capabilities — everything here is implemented and queryable on a
current `dev` build. Companion to [`ROADMAP.md`](ROADMAP.md), which tracks planned
and partially-implemented work.

For the authoritative, live list of scanned resource types, run
`disco coverage services --providers <aws|azure|gcp>` against your binary.

---

## Scanning

- **Per-service SDK discovery.** Every cloud service is scanned through its native
  Go SDK — no unified inventory APIs (AWS Resource Explorer, Azure Resource Graph,
  GCP Cloud Asset Inventory). This surfaces resources the unified APIs skip: KMS
  grants, EFS mount targets, CloudFormation-managed resources, IAM Identity Center
  assignments, Entra ID identities, GCP VPC Service Controls perimeters, and more.
- **AWS** — per-account, with `--regions`, `--profile`, and `--skip-globals`.
- **Azure** — per-subscription via `DefaultAzureCredential`; scans every accessible
  subscription, scoped per subscription / resource group.
- **GCP** — per-project fan-out across every reachable project; org/folder-scoped
  resources (IAM policies, log sinks, VPC-SC) run once per scan.
- **Partial-scan tolerance.** A denied or unreachable service degrades to a warning
  and a partial-scan status rather than failing the run; edges pointing at
  unscanned targets skip silently, so a partial scan still yields a usable graph.
- **Secret scrubbing** at the storage boundary (`store/sanitize.go`) — resource
  attributes are redacted before anything is written to the local database.
- **Stable resource identity.** Deterministic resource IDs keep rows stable across
  rescans; each `disco scan` records a row in the `scans` table for drift queries.

## Resource graph

- **Typed edges** connect resources during a resolve phase that runs after scanning:
  `contains`, `uses`, `attached-to`, `routes-to`, `assumes`, `peer`, `bounded-by`,
  plus the cross-boundary kinds `cross-account-trust` (AWS), `cross-sub-rbac`
  (Azure), `cross-project-iam` (GCP), and `org-iam` (GCP org/folder-scoped IAM
  policy grants, distinct from `cross-project-iam` since an org/folder-level
  grant has org-wide blast radius, not a two-project relationship).
- **Resolvers** read scanned rows and emit edges into `relationships` and a
  `hierarchy_closure` table — e.g. Lambda → KMS/subnet/SG, IAM policy documents →
  KMS/S3/Secrets/DynamoDB/…, ECS task-def → task & execution roles, CloudFront →
  S3 origin & Lambda@Edge, EventBridge rule → its targets.
- **Cross-account / cross-project trust.** Foreign-principal references resolve to
  the real resource when both sides are scanned, or to a synthetic foreign-account
  / foreign-subscription / foreign-project stub so the boundary stays visible.
- **Graph queries:**
  - `graph blast <id>` — outbound reachability from a seed, grouped by distance ring.
  - `graph path <A> <B>` — shortest path between two resources.
  - `graph complete` — the full graph; `--orphans-only` keeps just unattached nodes.
  - Partial-ID lookup (short IDs pasted from a ticket resolve cleanly), traversal
    filters (`--exclude-types` with suffix globs, `--exclude-regions`, `--max-nodes`,
    `--max-edges`, `--depth`, `--kinds`, `--direction`), and `--cluster` /
    `--label-template` for grouped, labelled diagram output.

## Query & reporting

- **`resources`** — filter by `--type`, `--providers`, `--regions` (both accept
  one or more comma-separated values), `--discovered-since`, and `--scan-id` /
  `--scan-as discovered|verified|any`. AWS-managed resources are hidden by
  default, opt-in via `--include-managed`. **`resources show <id|native-id|name>`**
  resolves and prints a single resource (same lookup as `graph` / `history`).
- **`summary`** — portfolio rollup by provider / account / region / type with an
  as-of timestamp.
- **`tag-coverage`** — per-tag-key coverage rate for cost-allocation hygiene;
  zero-coverage keys still appear so dashboards see the absent-tag signal.
- **`diff <scanA> <scanB>`** — what changed between two recorded scans.
- **`scans`** / **`scans show <id>`** — list recorded runs and inspect one.
- **Output formats** — table, JSON, JSONL, CSV, SARIF, DOT, Mermaid (per command).
  `resources -o json`, `graph complete -o json`, and `check -o json` are byte-identical
  across runs (same SHA-256), so they're safe to commit, diff, and feed into CI.

## Policy & compliance checks

- **`check`** — OPA Rego evaluation against the local resource DB.
- **Bundled pack** — `--packs aws-waf`, a 5-rule AWS Well-Architected sample.
- **Bring-your-own rules** — `--rules ./policies/` loads a custom Rego directory.
- **SARIF 2.1.0 output** (`-o sarif`) drops straight into GitHub / GitLab / Sonar
  code-scanning ingest; `partialFingerprints` de-dupe repeat findings across runs.
- **Exit-code gating** — any finding gates the exit code at 1 by default (wire
  straight into CI); `--exit-zero` publishes findings without breaking the pipeline.
  Filter by `--severity` and `--tag k=v`.
- **Findings persistence** — `check --persist` records a check run + findings to the
  DB; `disco findings list` / `disco findings runs` query them.

## Evidence snapshots

- **`snapshot <out>`** freezes the DB into a single archive — `.zip`, `.tar.gz`/
  `.tgz`, or `.tar.xz`/`.txz` (format follows the extension; xz is pure-Go) — with a
  manifest carrying the inner-DB SHA-256, generated-at timestamp, and scan IDs.
- **`verify <archive>`** re-checks the manifest and inner-DB hash.
- **Detached signing** — `snapshot --signing-payload` emits the canonical manifest
  bytes for an external signer; `verify --signature --pubkey` validates the ed25519
  signature. Pair with `--db-readonly` to guarantee the source DB isn't mutated.

## Coverage tooling

- **`coverage services`** — the scanner-declared type list from the running binary;
  `--filter uncovered` shows what each cloud's registry exposes that disco doesn't
  yet scan; `--check-strict` exits non-zero on drift against the live provider
  registry (CloudFormation `ListTypes` / Azure ARM `Providers` / GCP Discovery API)
  — a CI gate for new resource types.
- **`coverage resolvers`** — `--only-unannotated` surfaces resolvers with zero
  declared edges, the candidate sweep targets for closing graph gaps.

## Provider coverage

Broad coverage across all three clouds. Highlights below; run
`disco coverage services --providers <p>` for the live, per-binary list.

- **AWS** — EC2, IAM, S3, Lambda, RDS, EKS, ECS, KMS, Route53, ELBv2, CloudFront,
  CloudFormation, GuardDuty, Detective, Inspector v2, Macie, Backup, CloudTrail,
  Identity Center, Organizations, EventBridge, Step Functions, Secrets Manager,
  DynamoDB, SNS, SQS, EFS, WAFv2, ACM, Cognito, Kinesis, Firehose, Glue, Athena,
  App Runner, AppSync, MQ, AppFlow, Application Auto Scaling, Access Analyzer,
  Managed Prometheus, and many more.
- **Azure** — compute (VMs/VMSS/disks), networking (vNet, NSG, App Gateway, Front
  Door, ExpressRoute, vWAN, VPN, Traffic Manager, Private Endpoints, DNS), storage,
  Key Vault, SQL, App Service, AKS, Container Apps, ACR, Cosmos, Redis, Event Hub,
  Service Bus, Logic Apps, Synapse, APIM, Policy, RBAC, Log Analytics, Managed
  Identity, Resource Groups, Subscriptions/Management Groups, Entra ID, and more.
- **GCP** — Compute, Storage, IAM (incl. service accounts + key bindings), Cloud
  DNS, KMS, Pub/Sub, BigQuery, Bigtable, Firestore, Spanner, Cloud Functions Gen2,
  Cloud Run (services + jobs), Batch, Composer, Artifact Registry, Certificate
  Manager, Cloud Build, Cloud Armor, Load Balancing, Logging sinks, Monitoring
  alert policies, Secret Manager, Binary Authorization, VPC Service Controls, and
  the project/folder/org hierarchy.

---

See [`ROADMAP.md`](ROADMAP.md) for planned and in-progress work.
