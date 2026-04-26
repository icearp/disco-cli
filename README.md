# disco

Cloud resource discovery CLI. Scans AWS accounts, Azure subscriptions, and GCP organizations via per-service native SDK calls. Resolves cross-resource relationships and stores everything in a local SQLite graph for query, diff, and posture review.

## Why

Unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory) trade coverage for convenience. `disco` calls each service's native SDK directly so the graph reflects what actually exists, including the long tail of resource types those aggregators miss.

## Install

Pure-Go build, no CGO, single static binary.

```bash
CGO_ENABLED=0 go build -o disco .
```

Cross-compile from Linux:

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o dist/disco-linux-amd64   .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o dist/disco-darwin-arm64  .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/disco-windows-amd64.exe .
```

`CGO_ENABLED=0` is mandatory — storage uses `modernc.org/sqlite` (pure-Go transpile) so the binary needs no C toolchain on any target.

## Usage

```bash
disco scan aws    --profile myprofile
disco scan azure  --subscription <sub-id>
disco scan gcp    --org <org-id>

disco list  --type aws:ec2:instance
disco graph <resource-id> --kinds contains --depth 2
disco diff  --since 24h
disco check                       # run posture rules
```

Subcommands:

- `scan`   — enumerate resources for a provider, write to local DB
- `list`   — query stored resources by type, account, region, tag
- `graph`  — walk relationships from a starting resource
- `diff`   — compare two scan timestamps
- `check`  — evaluate rules engine against current state

## Configuration

Viper reads `~/.disco/config.yaml` and the `DISCO_` env-var prefix. Override the DB path with `--db` or `$DISCO_DB`; default is `~/.disco/disco.db`.

## Architecture

```
cmd/<subcommand>.go  →  internal/providers/<aws|azure|gcp>/  →  internal/store/
```

- **Providers** register scanners (cloud → resource rows) and resolvers (resource rows → relationship edges) via `init()`.
- **Store** is SQLite via `sqlx` + `squirrel`. Schema covers `resources`, `relationships`, and a hierarchy closure table.
- **Resource types** are namespaced lowercase: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

Path-scoped `CLAUDE.md` files document conventions in each subtree (provider scanner/resolver patterns, store schema, CLI orchestration).

## Coverage

AWS: 30+ services including EC2, IAM, S3, Lambda, RDS, EKS, ECS, KMS, Route53, ELBv2, CloudFront, CloudFormation, GuardDuty, Backup, CloudTrail, IAM Identity Center, Organizations.

Azure: compute, network, storage, key vault, SQL, app service, AKS.

GCP: compute, storage, IAM, project hierarchy.

See `ROADMAP.md` for in-flight scanners and gaps.

## Development

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go test ./internal/providers/aws/... -run TestSomething -v
go vet ./...
```

Primary branch: `dev`. Feature branches fork from `dev` and merge back.
