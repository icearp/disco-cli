# disco

`disco` is a CLI that pulls an inventory of your AWS, Azure, and GCP accounts into a local SQLite database, along with the relationships between resources. Once it's scanned, you can query the database offline to figure out things like what a given IAM role is attached to or which Lambdas read a particular secret. It's aimed at security and compliance work, where you usually need to see everything rather than the subset surfaced by a console search box.

## What it does

- `scan` walks an AWS account, Azure subscription, or GCP org and writes every resource it finds.
- A resolve phase runs as part of `scan`, connecting resources with typed edges (`contains`, `uses`, `attached-to`, `routes-to`, `assumes`, `peer`, `bounded-by`, plus `cross-account-trust` / `cross-sub-rbac` / `cross-project-iam`).
- `list`, `diff`, `graph`, `check`, `coverage`, `summary`, `tag-coverage`, `scans`, `snapshot`, and `verify` query the local DB without going back to the cloud.

## Why not Resource Explorer, Resource Graph, or Cloud Asset Inventory?

Those services are convenient, but they don't cover everything. `disco` calls each cloud's per-service SDK directly, so things that the unified APIs skip — KMS grants, EFS mount targets, CloudFormation-managed resources, IAM Identity Center assignments, Entra ID identities, GCP VPC Service Controls perimeters, and a fairly long list of others — actually show up in the graph.

## Why disco

- **Fast offline reads.** Sub-second `list` + `graph` queries against a ~3k-resource DB; ~100ms policy eval. The cloud is hit once at scan time, never at query time.
- **Deterministic output.** `list -o json`, `graph complete -o json`, `check -o json` are byte-stable across runs (matching SHA-256). Diffs are real diffs, not timestamp churn.
- **SARIF + OPA out of the box.** `disco check --output sarif` produces GitHub code-scanning-ingestible findings; bring your own Rego, or use the bundled `aws-waf` pack.
- **Coverage drift signal.** `disco coverage --check-strict` with `covered` / `uncovered` / `synthetic` / `upstream-missing` taxonomy catches when a cloud ships a new resource type before disco does.
- **Forensic-friendly.** Pure-Go SQLite (CGO-free), DB at `0600`, single-file `disco snapshot` archives with manifest + inner-DB SHA-256. Trivially portable for evidence handoff.
- **Composable formats.** JSON, JSONL, CSV, SARIF, DOT, Mermaid. Pipes into whatever the next tool expects.

## Install

You need Go and that's it. There's no C toolchain involved because the SQLite driver is pure Go (`modernc.org/sqlite`), which is also why `CGO_ENABLED=0` is required (the Makefile sets it for you):

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

# Policy check (bundled aws-waf sample pack; --rules accepts a custom Rego dir)
disco check --packs aws-waf --output sarif > findings.sarif

# Evidence archive (single-file; format from extension: .zip|.tar.gz|.tar.xz)
disco snapshot evidence-2026-05-06.tar.xz
disco verify   evidence-2026-05-06.tar.xz
```

Azure scopes per accessible subscription (override via config); GCP fans out across accessible projects (override via config). Neither takes a scope flag on `scan` — credentials drive which subs/projects are reachable.

Resource types follow the pattern `cloud:service:kind`, lowercase. So `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`, and so on.

## Configuration

Config lives at `$XDG_CONFIG_HOME/disco/config.yaml` (Viper format) — `~/.config/disco/config.yaml` on Linux, the platform app-data dir on macOS/Windows. Anything in the file can be overridden with a `DISCO_`-prefixed environment variable. The database path defaults to `$XDG_DATA_HOME/disco/disco.db` (`~/.local/share/disco/disco.db` on Linux); override it with `--db` or `$DISCO_DB`.

## How it works

```
cmd/<subcommand>.go  →  internal/providers/<aws|azure|gcp>/  →  internal/store/  →  sqlite
                              (scanners then resolvers)         (sqlx + squirrel)
```

Scanners are registered via `init()` and write rows into the `resources` table, one file per service. Resolvers run afterwards, read those rows, and emit edges into `relationships` and `hierarchy_closure`. Edges that point at unscanned targets get skipped rather than failing, so a partial scan still gives you a usable graph instead of a wall of FK errors. Secrets are scrubbed at the store boundary in `internal/store/sanitize.go` before anything is written.

There are `CLAUDE.md` files scattered through the tree that document the conventions for each subdirectory; `CODE_STRUCTURE.md` is the higher-level map.

## Coverage

All three clouds are covered broadly. Run `disco coverage --provider <aws|azure|gcp>` for the live, scanner-declared list (matches the running binary; updates with the code).

- **AWS** — the broadest surface: EC2, IAM, S3, Lambda, RDS, EKS, ECS, KMS, Route53, ELBv2, CloudFront, CloudFormation, GuardDuty, Detective, Inspector v2, Macie, Backup, CloudTrail, Identity Center, Organizations, EventBridge, Step Functions, Secrets Manager, DynamoDB, SNS, SQS, EFS, WAFv2, ACM, Cognito, Kinesis, Firehose, Glue, Athena, App Runner, AppSync, MQ, AppFlow, Application Auto Scaling, AccessAnalyzer, Managed Prometheus, plus more.
- **Azure** — compute (VMs/VMSS/disks), networking (vNet, NSG, AGW, Front Door, ER, vWAN, VPN, Traffic Manager, Private Endpoints, DNS), storage, Key Vault, SQL, App Service, AKS, Container Apps, ACR, Cosmos, Redis, EventHub, ServiceBus, Logic Apps, Synapse, APIM, Policy, RBAC, Log Analytics, ManagedIdentity, ResourceGroups, Subscriptions/MgmtGroups, Entra ID, and more — run `disco coverage --provider azure` for the live list.
- **GCP** — Compute, Storage, IAM (incl. service accounts + key bindings), Cloud DNS, KMS, Pub/Sub, BigQuery, Bigtable, Firestore, Spanner, Cloud Functions Gen2, Cloud Run (services + jobs), Batch, Composer, Artifact Registry, Cert Manager, Cloud Build, Cloud Armor, Load Balancing, Logging sinks, Monitoring alert policies, Secret Manager, Binary Authorization, VPC Service Controls, project/folder/org hierarchy.

`disco coverage --filter uncovered` shows what each cloud's registry exposes that disco does not yet scan. `FEATURES.md` lists shipped capabilities; `ROADMAP.md` tracks planned work.

## Development

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go test ./internal/providers/aws/... -run TestSomething -v
go vet ./...
```

The primary branch is `dev`. Feature branches fork from `dev` and merge back into it.

## Acknowledgements

Large portions of this codebase were written with [Claude Code](https://claude.com/claude-code), Anthropic's CLI for Claude. Scanner and resolver scaffolding, test fixtures, and a fair amount of the cross-service edge logic were drafted, reviewed, and iterated on with it.
