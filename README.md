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

JSON, JSONL, CSV, SARIF, DOT, and Mermaid output are available for the queryable verbs. `list -o json`, `graph complete -o json`, and `check -o json` produce identical bytes across runs (same SHA-256), which makes them safe to commit, diff, and feed into CI. `disco check --output sarif` drops straight into GitHub code scanning. `disco snapshot` packs the DB into a single file with a manifest and inner-DB hash for handoff. `disco coverage --check-strict` flags new cloud resource types disco doesn't yet scan.

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

## Quickstart

```bash
# Scan
disco scan aws    --profile myprofile --regions us-east-1,us-west-2
disco scan azure  --services azure:compute,azure:network
disco scan gcp    --services gcp:compute,gcp:storage

# Query
disco list  --type aws:ec2:instance --region us-east-1
disco graph <resource-id> --kinds contains --depth 2 --output dot
disco coverage --provider aws

# Policy check; --rules accepts a custom Rego directory
disco check --packs aws-waf --output sarif > findings.sarif

# Evidence archive; format follows the extension (.zip, .tar.gz, .tar.xz)
disco snapshot evidence-2026-05-06.tar.xz
disco verify   evidence-2026-05-06.tar.xz
```

Azure scans every accessible subscription, GCP fans out across accessible projects. Override via config. Resource types are lowercase `cloud:service:kind`: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

## Configuration

Config lives at `$XDG_CONFIG_HOME/disco/config.yaml` (Viper format). On Linux that's `~/.config/disco/config.yaml`; macOS and Windows use the platform app-data dir. Any key can be overridden with a `DISCO_`-prefixed env var. The DB defaults to `$XDG_DATA_HOME/disco/disco.db` (`~/.local/share/disco/disco.db` on Linux); override with `--db` or `$DISCO_DB`.

## How it works

```
cmd/<subcommand>.go  →  internal/providers/<aws|azure|gcp>/  →  internal/store/  →  sqlite
                              (scanners then resolvers)         (sqlx + squirrel)
```

Scanners register via `init()` and write rows into `resources`, one file per service. Resolvers run after, read those rows, and emit edges into `relationships` and `hierarchy_closure`. Edges pointing at unscanned targets skip silently rather than failing the scan, so a partial scan still produces a usable graph. Secrets are scrubbed at the store boundary in `internal/store/sanitize.go`.

Per-subdirectory `CLAUDE.md` files document local conventions; `CODE_STRUCTURE.md` is the higher-level map.

## Coverage

All three clouds are covered broadly. `disco coverage --provider <aws|azure|gcp>` prints the scanner-declared list from the running binary.

- **AWS**: EC2, IAM, S3, Lambda, RDS, EKS, ECS, KMS, Route53, ELBv2, CloudFront, CloudFormation, GuardDuty, Detective, Inspector v2, Macie, Backup, CloudTrail, Identity Center, Organizations, EventBridge, Step Functions, Secrets Manager, DynamoDB, SNS, SQS, EFS, WAFv2, ACM, Cognito, Kinesis, Firehose, Glue, Athena, App Runner, AppSync, MQ, AppFlow, Application Auto Scaling, AccessAnalyzer, Managed Prometheus, plus more.
- **Azure**: compute (VMs/VMSS/disks), networking (vNet, NSG, AGW, Front Door, ER, vWAN, VPN, Traffic Manager, Private Endpoints, DNS), storage, Key Vault, SQL, App Service, AKS, Container Apps, ACR, Cosmos, Redis, EventHub, ServiceBus, Logic Apps, Synapse, APIM, Policy, RBAC, Log Analytics, ManagedIdentity, ResourceGroups, Subscriptions/MgmtGroups, Entra ID, and more. Run `disco coverage --provider azure` for the live list.
- **GCP**: Compute, Storage, IAM (incl. service accounts + key bindings), Cloud DNS, KMS, Pub/Sub, BigQuery, Bigtable, Firestore, Spanner, Cloud Functions Gen2, Cloud Run (services + jobs), Batch, Composer, Artifact Registry, Cert Manager, Cloud Build, Cloud Armor, Load Balancing, Logging sinks, Monitoring alert policies, Secret Manager, Binary Authorization, VPC Service Controls, project/folder/org hierarchy.

`disco coverage --filter uncovered` shows what each cloud's registry exposes that disco does not yet scan. `FEATURES.md` lists shipped capabilities; `ROADMAP.md` tracks planned work.

## Development

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go test ./internal/providers/aws/... -run TestSomething -v
go vet ./...
```

The primary branch is `dev`. Feature branches fork from `dev` and merge back into it.

## Acknowledgements

The vast majority of this codebase was written by Claude (Anthropic) using [Claude Code](https://claude.com/claude-code), under human direction and review. Architecture decisions, scope, and final commits are mine; the line-level work — scanners, resolvers, tests, edge logic, docs — is overwhelmingly Claude-authored. Co-author trailers on commits reflect this.
