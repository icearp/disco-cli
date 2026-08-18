# disco

`disco` is a CLI for scanning AWS, Azure, and GCP accounts into a local SQLite database, including the relationships between resources. After scanning, you query the DB offline: which Lambdas read a given secret, what an IAM role is attached to, which RDS clusters share a KMS key. Built for security and compliance work that needs full inventory, not the slice a console search returns.

## What it does

- `scan` walks an AWS account, Azure subscription, or GCP org and writes every resource it finds.
- `scan` also runs a resolve phase that connects resources with typed edges (`contains`, `uses`, `attached-to`, `routes-to`, `assumes`, `peer`, `bounded-by`, plus `cross-account-trust` / `cross-sub-rbac` / `cross-project-iam` / `org-iam`).
- `resources`, `diff`, `graph`, `check`, `findings`, `history`, `coverage`, `summary`, `tag-coverage`, `scans`, `snapshot`, and `verify` query the local DB without going back to the cloud.

## Why not Resource Explorer, Resource Graph, or Cloud Asset Inventory?

Those services are convenient but incomplete. `disco` calls each cloud's per-service SDK directly, so the things unified APIs skip still show up in the graph: KMS grants, EFS mount targets, CloudFormation-managed resources, IAM Identity Center assignments, Entra ID identities, GCP VPC Service Controls perimeters, and plenty more.

## Why disco

A scan takes a few minutes. After that, queries run sub-second against a few-thousand-resource DB, and cloud APIs only get hit at scan time, so `resources`, `graph`, and `check` work the same on a plane as they do on the office network.

The queryable verbs speak JSON, JSONL, CSV, SARIF, DOT, and Mermaid. `resources -o json`, `graph complete -o json`, and `check -o json` produce identical bytes across runs (same SHA-256), so they're safe to commit, diff, and feed into CI. `disco check --output sarif` drops straight into GitHub code scanning. `disco snapshot` packs the DB into a single file with a manifest and inner-DB hash for handoff, and `disco coverage services --check-strict` flags new cloud resource types disco doesn't yet scan.

## Hosted

`disco` is complete on its own: MIT-licensed, runs on your machine, scans into a single local file, and answers every query offline afterward. It has no telemetry and no paid edition. Several of the verbs below are CLI-only and have no hosted equivalent today: shortest-path queries between two resources, scan-to-scan diff, orphan detection, per-key tag-coverage, OPA/Rego evaluation with SARIF output and exit-code gating, and byte-identical JSON output.

[Disco Cloud](https://discocloud.io) is a hosted service built on this scanner, for teams that would rather not run it themselves. It adds hosting isolated per organization, guided connection for AWS and Google Cloud, scans that run on its infrastructure instead of a laptop, a web UI, SSO, webhooks, an audit log, and cost allocation. Azure accounts are scanned the same way but are connected by arrangement rather than self-serve today. It is free for 3 cloud accounts with no card and no trial clock, and priced per cloud account rather than per seat above that.

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
├── resources                 filter by --type, --providers, --regions, --discovered-since
│   └── show <id|name>        full record for one resolved resource
├── diff <from> <to>          resources added / gone stale between two scan runs
├── history <id|name>         every recorded version of one resource, oldest→newest
├── graph
│   ├── blast <id>            reachability from a seed
│   ├── path <A> <B>          shortest path between two resources
│   └── complete              full graph (every resource + edges)
├── check                     OPA Rego policy eval; --packs, --rules, --output sarif, --persist
├── findings                  query findings recorded by `check --persist`
│   ├── list                  persisted findings (defaults to latest run)
│   └── runs                  recorded check-run history
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
disco resources  --type aws:ec2:instance --regions us-east-1,us-west-2
disco resources show i-0abc123 -o json
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

Coverage rate per tag key, which is the precondition for chargeback and showback. Zero-coverage keys still appear so dashboards see the absent-tag signal. Tag KEYS shaped like AWS access-key IDs are flagged `[suspicious:aws-access-key-id]` so credential paste-into-tag mistakes don't render as legitimate scorecard rows.

```bash
disco tag-coverage owner cost-center environment --case-insensitive
disco tag-coverage --type aws:ec2:instance -o json | jq '.[] | select(.coverage<0.95)'
```

### CI policy gate (SARIF → GitHub code-scanning)

OPA Rego evaluation against the local resource DB. SARIF 2.1.0 output feeds directly into GitHub, GitLab, or Sonar code-scanning ingest. Any reported finding gates the exit code at 1 by default, so wiring `disco check` into a CI step needs no extra flag. `--exit-zero` opts out for inventory-only runs that should publish findings without breaking the pipeline. `partialFingerprints` keeps repeat findings de-duped across runs. BYO Rego via `--rules ./policies/`; bundled `aws-waf` is a 5-rule sample pack.

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

Every `disco scan` records a row in `scans`. `--scan-id latest` resolves to the most-recent scan that touched rows. `--scan-as discovered|verified|any` picks which scan-FK column the filter targets: `discovered` for "what's new this run", `verified` for "what this run re-verified."

```bash
disco scans
disco resources --scan-id latest --scan-as discovered
disco resources --discovered-since 2026-04-01 -o json | jq 'length'
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

### Supply-chain SBOMs

Every tagged release ships a Software Bill of Materials **per binary**, in both formats:
`disco-<version>-<os>-<arch>.cdx.json` (CycloneDX 1.7) and `.spdx.json` (SPDX 2.3), alongside
the existing `.sha256` sidecar. They are generated by [syft](https://github.com/anchore/syft)
from the binary's Go buildinfo (before compression), so they list exactly the modules linked
into that binary. Feed the CycloneDX doc to Dependency-Track / Grype, or the SPDX doc to
license/compliance tooling. Generate the same locally with `make sbom` (writes to `dist/`).

### Vulnerability gating on release

Tagged releases are gated on [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck):
the CI `test` job runs a symbol-level reachability scan against the same build config disco ships
(`-tags grpcnotrace`, `CGO_ENABLED=0`), and a **reachable** known vulnerability fails the job so
no draft release is cut. The vulnerability DB ([vuln.go.dev](https://vuln.go.dev)) is queried
live at scan time. Run the same check locally with `make vulncheck`.

The scan uses `-mode binary` over a freshly built binary rather than source mode — source mode
builds whole-program SSA across disco's ~496-module dependency graph (three full cloud SDKs) and
needs more than 23GB of RAM. Binary mode reads the symbol table instead, keeping the same
symbol-level precision under 8GB.

### Coverage drift gating in CI

`coverage --check-strict` exits non-zero whenever the scanner-declared type list disagrees with the live cloud-provider registry (CloudFormation `ListTypes` / Azure ARM `Providers/List` / GCP Discovery API). Pair with `--resolvers --only-unannotated` to surface resolvers with zero declared `EdgeDecl`. Those are the candidate sweep targets for closing graph gaps.

```bash
disco coverage services --check-strict --providers aws
disco coverage resolvers --only-unannotated --providers aws -o json | jq '.[].resolver'
```

### Find dangling resources mid-incident

`graph complete --orphans-only` keeps only nodes with zero in/out edges in the returned set. That surfaces unattached EBS volumes, key-pairs no instance uses, and IAM principals with no group or policy attachments.

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

All three clouds are covered broadly: hundreds of services scanned via each cloud's
per-service SDK, spanning compute, storage, networking, identity, data, security, and
governance. Approximate breadth (distinct resource types the running binary declares):

| Provider | Services | Resource types |
|----------|---------:|---------------:|
| AWS      |    ~300  |         ~1,800 |
| Azure    |    ~150  |           ~420 |
| GCP      |     ~40  |           ~250 |

These lists move as services ship, so the README does not hand-maintain them. For the live,
authoritative list from the binary you built, run:

```bash
disco coverage services --providers aws        # or azure, gcp
disco coverage services --filter uncovered     # what each registry exposes that disco doesn't yet scan
```

Detecting drift between disco's scanners and the upstream cloud registries is exactly what the
`coverage` command exists for (see the coverage-gating use case above). `FEATURES.md` lists
shipped capabilities in prose.

## Development

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go test ./internal/providers/aws/... -run TestSomething -v
go vet ./...
```

The primary branch is `main`. Feature branches fork from `main` and merge back into it.

## Releases

Tagged `v*` pushes trigger the GitHub Actions release workflow (`.github/workflows/release.yaml`), which cross-compiles and
publishes binaries for linux/darwin/windows on amd64+arm64. Builds are version-stamped
from the tag. You can always build from source with `make build`.

## Contributing

Contributions are welcome. See [`CONTRIBUTING.md`](CONTRIBUTING.md) for build, test,
and branch conventions, and [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) for community
expectations. Found a security issue? Please report it privately per
[`SECURITY.md`](SECURITY.md) rather than opening a public issue.

## License

MIT — see [`LICENSE`](LICENSE).

## Acknowledgements

The vast majority of this codebase was written by Claude (Anthropic) using [Claude Code](https://claude.com/claude-code), under human direction and review. Architecture decisions, scope, and final commits are mine; the line-level work — scanners, resolvers, tests, edge logic, docs — is overwhelmingly Claude-authored. Co-author trailers on commits reflect this.
