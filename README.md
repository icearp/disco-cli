# disco

`disco` is a CLI for scanning AWS, Azure, and GCP accounts into a local SQLite database, including the relationships between resources. After scanning, you query the DB offline: which Lambdas read a given secret, what an IAM role is attached to, which RDS clusters share a KMS key. Built for security and compliance work that needs full inventory, not the slice a console search returns.

## What it does

- `scan` walks an AWS account, Azure subscription, or GCP org and writes every resource it finds.
- `scan` also runs a resolve phase that connects resources with typed edges (`contains`, `uses`, `attached-to`, `routes-to`, `assumes`, `peer`, `bounded-by`, plus `cross-account-trust` / `cross-sub-rbac` / `cross-project-iam`).
- `list`, `diff`, `graph`, `check`, `coverage`, `summary`, `tag-coverage`, `scans`, `snapshot`, and `verify` query the local DB without going back to the cloud.

## Why not Resource Explorer, Resource Graph, or Cloud Asset Inventory?

Those services are convenient but incomplete. `disco` calls each cloud's per-service SDK directly, so the things unified APIs skip — KMS grants, EFS mount targets, CloudFormation-managed resources, IAM Identity Center assignments, Entra ID identities, GCP VPC Service Controls perimeters, and others — show up in the graph.

## Why disco

Scans take minutes; queries are sub-second against a few-thousand-resource DB. Cloud APIs only get hit at scan time, so `list` / `graph` / `check` work the same on a plane as on the office network.

JSON, JSONL, CSV, SARIF, DOT, and Mermaid output are available for the queryable verbs. `list -o json`, `graph complete -o json`, and `check -o json` produce identical bytes across runs (same SHA-256), which makes them safe to commit, diff, and feed into CI. `disco check --output sarif` drops straight into GitHub code scanning. `disco snapshot` packs the DB into a single file with a manifest and inner-DB hash for handoff. `disco coverage services --check-strict` flags new cloud resource types disco doesn't yet scan.

## Install

Build needs Go. No C toolchain: the SQLite driver is pure Go (`modernc.org/sqlite`), so `CGO_ENABLED=0` is required. The Makefile sets it.

```bash
make build           # → ./disco, version-stamped from `git describe`
make all             # fmt + vet + test + build
make test            # CGO_ENABLED=0 go test ./...
make dist            # cross-compile linux/darwin/windows amd64+arm64 into dist/
```

Plain `go build` works too if you skip the Makefile, but the version stamp falls back to `dev` (the `-X cmd.Version` ldflag is injected by `make`):

```bash
CGO_ENABLED=0 go build -o disco .
```

## Commands

```
disco
├── scan
│   ├── aws                   per-account, --regions, --profile, --skip-globals
│   ├── azure                 per-subscription via DefaultAzureCredential
│   └── gcp                   per-project fan-out across reachable projects
├── list                      filter by --type, --provider, --region, --discovered-since
├── graph
│   ├── blast <id>            reachability from a seed
│   ├── path <A> <B>          shortest path between two resources
│   └── complete              full graph (every resource + edges)
├── check                     OPA Rego policy eval; --packs, --rules, --output sarif
├── coverage                  scanner-declared types vs cloud registry; --check-strict
├── summary                   portfolio rollup
├── tag-coverage              per-tag coverage rate
├── scans
│   └── show <id>             detail for one recorded scan run
├── snapshot <out>            freeze DB into single-file archive (.zip|.tar.gz|.tar.xz)
├── verify <archive>          verify snapshot manifest + inner-DB SHA-256
├── config
│   └── init                  write a boilerplate config.yaml
└── completion                shell completion script (bash|zsh|fish|powershell)
```

Every command takes `--db` / `--config` / `-v`. `--db-readonly` opens the DB read-only and rejects any write path (scan, snapshot to a write-locked DB, etc.).

## Quickstart

```bash
# Scan
disco scan aws    --profile myprofile --regions us-east-1,us-west-2
disco scan azure  --services azure:compute,azure:network
disco scan gcp    --services gcp:compute,gcp:storage

# Query
disco list  --type aws:ec2:instance --region us-east-1
disco graph <resource-id> --kinds contains --depth 2 --output dot
disco coverage services --providers aws

# Policy check; --rules accepts a custom Rego directory
disco check --packs aws-waf --output sarif > findings.sarif

# Evidence archive; format follows the extension (.zip, .tar.gz, .tar.xz)
disco snapshot evidence-2026-05-06.tar.xz
disco verify   evidence-2026-05-06.tar.xz
```

Azure scans every accessible subscription, GCP fans out across accessible projects. Override via config. Resource types are lowercase `cloud:service:kind`: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

## Use cases

The CLI is shaped around a handful of recurring jobs. Each block is one tested invocation; chain with `jq` / `awk` / your own scripts as needed.

### Portfolio rollup ("what do we own?")

One-page count of the estate by provider, account, region, and resource type, with an as-of timestamp from the most recent scan. Output is also available as JSON or CSV for slide builders.

```bash
disco summary
disco summary -o json | jq '.by_account, .by_provider'
```

### Tag-hygiene scorecard for cost-allocation

Coverage rate per tag key — the precondition for chargeback / showback. Zero-coverage keys still appear so dashboards see the absent-tag signal. Tag KEYS shaped like AWS access-key IDs are flagged `[suspicious:aws-access-key-id]` so credential paste-into-tag mistakes don't render as legitimate scorecard rows.

```bash
disco tag-coverage owner cost-center environment --case-insensitive
disco tag-coverage --type aws:ec2:instance -o json | jq '.[] | select(.coverage<0.95)'
```

### CI policy gate (SARIF → GitHub code-scanning)

OPA Rego evaluation against the local resource DB; SARIF 2.1.0 output drops straight into GitHub / GitLab / Sonar code-scanning ingest. Any reported finding gates the exit code at 1 by default — wiring `disco check` straight into a CI step needs no extra flag. `--exit-zero` opts out for inventory-only runs that should publish findings without breaking the pipeline. `partialFingerprints` keeps repeat findings de-duped across runs. BYO Rego via `--rules ./policies/`; bundled `aws-waf` is a 5-rule sample pack.

```bash
disco check --packs aws-waf --severity high -o sarif > findings.sarif
disco check --rules ./policies --tag waf_pillar=security -o table
disco check --packs aws-waf --exit-zero -o sarif > inventory.sarif   # render only
```

### Blast radius from a compromised IAM principal

BFS reachability from a seed. Partial-ID lookup means short IDs pasted from a ticket / CloudTrail line resolve cleanly. IAM principals receive edges rather than emit them, so `graph blast` auto-expands to `--direction both` for them.

```bash
disco graph blast 8895a0bd                       # short ID from a ticket
disco graph blast my-role --provider aws --type aws:iam:role --depth 4 -o dot | dot -Tpng > blast.png
```

### Drift detection between scans

Every `disco scan` records a row in `scans`. `--scan-id latest` resolves to the most-recent scan that touched rows. `--scan-as discovered|verified|any` picks which scan-FK column the filter targets — `discovered` for "what's new this run", `verified` for "what this run re-verified."

```bash
disco scans
disco list --scan-id latest --scan-as discovered
disco list --discovered-since 2026-04-01 -o json | jq 'length'
```

### Evidence package handoff (read-only snapshot + signed verify)

`disco snapshot` freezes the DB into a single archive (`.zip`, `.tar.gz`/`.tgz`, `.tar.xz`/`.txz`) with a manifest carrying the inner-DB SHA-256, generated-at timestamp, and scan IDs. `--db-readonly` guarantees the source DB is not mutated. `--signing-payload` emits the canonical manifest bytes for an external signer (`openssl pkeyutl -sign`, `minisign`, `ssh-keygen -Y sign`, cosign). The receiver's `disco verify --signature --pubkey` validates the detached ed25519 signature.

```bash
# Producer
disco --db-readonly snapshot /tmp/audit-2026-q2.tar.gz --signing-payload /tmp/audit.payload
openssl pkeyutl -sign -inkey priv.pem -rawin -in /tmp/audit.payload -out /tmp/audit.sig

# Auditor
disco verify /tmp/audit-2026-q2.tar.gz --signature /tmp/audit.sig --pubkey ed25519.pem
```

### Coverage drift gating in CI

`coverage --check-strict` exits non-zero whenever the scanner-declared type list disagrees with the live cloud-provider registry (CloudFormation `ListTypes` / Azure ARM `Providers/List` / GCP Discovery API). Pair with `--resolvers --only-unannotated` to surface resolvers with zero declared `EdgeDecl` — the candidate sweep targets for closing graph gaps.

```bash
disco coverage services --check-strict --providers aws
disco coverage resolvers --only-unannotated --providers aws -o json | jq '.[].resolver'
```

### Find dangling resources mid-incident

`graph complete --orphans-only` keeps only nodes with zero in/out edges in the returned set — surfaces unattached EBS volumes, key-pairs no instance uses, IAM principals with no group/policy attachments.

```bash
disco graph complete --orphans-only -o json | jq -r '.nodes[].resource | [.type, .name, .native_id] | @tsv'
```

## Configuration

Config lives at `$XDG_CONFIG_HOME/disco/config.yaml` (Viper format). On Linux that's `~/.config/disco/config.yaml`; macOS and Windows use the platform app-data dir. Any key can be overridden with a `DISCO_`-prefixed env var. The DB defaults to `$XDG_DATA_HOME/disco/disco.db` (`~/.local/share/disco/disco.db` on Linux); override with `--db` or `$DISCO_DB`.

## How it works

```
cmd/<subcommand>.go  →  internal/providers/<aws|azure|gcp>/  →  store/  →  sqlite
                              (scanners then resolvers)         (sqlx + squirrel)
```

Scanners register via `init()` and write rows into `resources`, one file per service. Resolvers run after, read those rows, and emit edges into `relationships` and `hierarchy_closure`. Edges pointing at unscanned targets skip silently rather than failing the scan, so a partial scan still produces a usable graph. Secrets are scrubbed at the store boundary in `store/sanitize.go`.

Per-subdirectory `CLAUDE.md` files document local conventions; `CODE_STRUCTURE.md` is the higher-level map.

## Coverage

All three clouds are covered broadly. `disco coverage services --providers <aws|azure|gcp>` prints the scanner-declared list from the running binary.

- **AWS**: EC2, IAM, S3, Lambda, RDS, EKS, ECS, KMS, Route53, ELBv2, CloudFront, CloudFormation, GuardDuty, Detective, Inspector v2, Macie, Backup, CloudTrail, Identity Center, Organizations, EventBridge, Step Functions, Secrets Manager, DynamoDB, SNS, SQS, EFS, WAFv2, ACM, Cognito, Kinesis, Firehose, Glue, Athena, App Runner, AppSync, MQ, AppFlow, Application Auto Scaling, AccessAnalyzer, Managed Prometheus, plus more.
- **Azure**: compute (VMs/VMSS/disks), networking (vNet, NSG, AGW, Front Door, ER, vWAN, VPN, Traffic Manager, Private Endpoints, DNS), storage, Key Vault, SQL, App Service, AKS, Container Apps, ACR, Cosmos, Redis, EventHub, ServiceBus, Logic Apps, Synapse, APIM, Policy, RBAC, Log Analytics, ManagedIdentity, ResourceGroups, Subscriptions/MgmtGroups, Entra ID, and more. Run `disco coverage services --providers azure` for the live list.
- **GCP**: Compute, Storage, IAM (incl. service accounts + key bindings), Cloud DNS, KMS, Pub/Sub, BigQuery, Bigtable, Firestore, Spanner, Cloud Functions Gen2, Cloud Run (services + jobs), Batch, Composer, Artifact Registry, Cert Manager, Cloud Build, Cloud Armor, Load Balancing, Logging sinks, Monitoring alert policies, Secret Manager, Binary Authorization, VPC Service Controls, project/folder/org hierarchy.

`disco coverage services --filter uncovered` shows what each cloud's registry exposes that disco does not yet scan. `FEATURES.md` lists shipped capabilities; `ROADMAP.md` tracks planned work.

## Development

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go test ./internal/providers/aws/... -run TestSomething -v
go vet ./...
```

The primary branch is `dev`. Feature branches fork from `dev` and merge back into it.

## Releases

Tagged `v*` pushes trigger the `.forgejo` release workflow, which cross-compiles and
publishes binaries for linux/darwin/windows on amd64+arm64. Builds are version-stamped
from the tag. You can always build from source with `make build`.

## Contributing

Contributions are welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md) for build, test,
and branch conventions, and [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) for community
expectations. Found a security issue? Please report it privately per
[`SECURITY.md`](SECURITY.md) rather than opening a public issue.

## License

MIT — see [`LICENSE`](LICENSE).

## Acknowledgements

The vast majority of this codebase was written by Claude (Anthropic) using [Claude Code](https://claude.com/claude-code), under human direction and review. Architecture decisions, scope, and final commits are mine; the line-level work — scanners, resolvers, tests, edge logic, docs — is overwhelmingly Claude-authored. Co-author trailers on commits reflect this.
