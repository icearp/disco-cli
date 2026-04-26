# disco

`disco` is a CLI that pulls an inventory of your AWS, Azure, and GCP accounts into a local SQLite database, along with the relationships between resources. Once it's scanned, you can query the database offline to figure out things like what a given IAM role is attached to or which Lambdas read a particular secret. It's aimed at security and compliance work, where you usually need to see everything rather than the subset surfaced by a console search box.

## What it does

- `scan` walks an AWS account, Azure subscription, or GCP org and writes every resource it finds.
- `resolve` runs after scanning and connects resources with typed edges (`contains`, `uses`, `attached-to`, `routes-to`, `assumes`, `peer`).
- `list` and `graph` query the local DB without going back to the cloud.
- `check` runs rules against stored state and prints findings.

## Why not Resource Explorer, Resource Graph, or Cloud Asset Inventory?

Those services are convenient, but they don't cover everything. `disco` calls each cloud's per-service SDK directly, so things that the unified APIs skip — KMS grants, EFS mount targets, CloudFormation-managed resources, IAM Identity Center assignments, and a fairly long list of others — actually show up in the graph.

## Install

You need Go and that's it. There's no C toolchain involved because the SQLite driver is pure Go (`modernc.org/sqlite`), which is also why `CGO_ENABLED=0` is required:

```bash
CGO_ENABLED=0 go build -o disco .
```

Cross-compile from anywhere to anywhere:

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o dist/disco-linux-amd64       .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o dist/disco-darwin-arm64      .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/disco-windows-amd64.exe .
```

## Quickstart

```bash
# Scan
disco scan aws    --profile myprofile
disco scan azure  --subscription <sub-id>
disco scan gcp    --org <org-id>

# Query
disco list  --type aws:ec2:instance --region us-east-1
disco graph <resource-id> --kinds contains --depth 2 --output dot
disco check
```

Resource types follow the pattern `cloud:service:kind`, lowercase. So `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`, and so on.

## Configuration

Config lives in `~/.disco/config.yaml` (Viper format). Anything in the file can be overridden with a `DISCO_`-prefixed environment variable. The database path defaults to `~/.disco/disco.db`; override it with `--db` or `$DISCO_DB`.

## How it works

```
cmd/<subcommand>.go  →  internal/providers/<aws|azure|gcp>/  →  internal/store/  →  sqlite
                              (scanners then resolvers)         (sqlx + squirrel)
```

Scanners are registered via `init()` and write rows into the `resources` table, one file per service. Resolvers run afterwards, read those rows, and emit edges into `relationships` and `hierarchy_closure`. Edges that point at unscanned targets get skipped rather than failing, so a partial scan still gives you a usable graph instead of a wall of FK errors. Secrets are scrubbed at the store boundary in `internal/store/sanitize.go` before anything is written.

There are `CLAUDE.md` files scattered through the tree that document the conventions for each subdirectory; `CODE_STRUCTURE.md` is the higher-level map.

## Coverage

AWS is the most thorough at the moment. Roughly 30+ services: EC2, IAM, S3, Lambda, RDS, EKS, ECS, KMS, Route53, ELBv2, CloudFront, CloudFormation, GuardDuty, Backup, CloudTrail, IAM Identity Center, Organizations, EventBridge, Step Functions, Secrets Manager, DynamoDB, SNS, SQS, EFS, WAFv2, ACM, Cognito, Kinesis, Firehose, plus a handful of others.

Azure covers compute, network, storage, key vault, SQL, app service, and AKS.

GCP covers compute, storage, IAM, and the project hierarchy.

`ROADMAP.md` tracks what's in progress and what's missing.

## Development

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go test ./internal/providers/aws/... -run TestSomething -v
go vet ./...
```

The primary branch is `dev`. Feature branches fork from `dev` and merge back into it.

## Acknowledgements

Large portions of this codebase were written with [Claude Code](https://claude.com/claude-code), Anthropic's CLI for Claude. Scanner and resolver scaffolding, test fixtures, and a fair amount of the cross-service edge logic were drafted, reviewed, and iterated on with it.
