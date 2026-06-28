# CLAUDE.md — `internal/coverage/`

Coverage matrix engine for `disco coverage`. Per-provider impl in `internal/providers/<p>/coverage.go` registers via `coverage.Register(...)` from init.

## Adding a provider

1. Implement `coverage.Provider` (Name, Fetch, Emits, Aliases, AlgorithmicKey).
2. Sweep every scanner's `registerService` to add `emits []coverage.TypeDecl{{Service, DiscoType, Uncatalogued, Leaf}}`.
3. Build alias map for known disco↔upstream mismatches; algorithmic fallback covers the rest.
4. Flag SDK-scanned types absent from the registry `Uncatalogued`, so they don't trip `upstream-missing`.

## Bucket semantics

- `covered` — disco emits + upstream registry has it.
- `uncovered` — upstream has it, no disco scanner.
- `uncatalogued` — a **real resource disco scans** via the SDK that no upstream registry lists (e.g. `aws:kms:grant`, GuardDuty/Detective/Inspector members, Azure Entra identities + SQL/network proxy children). Checked only on the no-match path, so it **auto-upgrades to `covered`** if the registry later lists the key (this is why `gcp:iam:policy` lands `covered` — Discovery's `iam.googleapis.com/Policy` matches it). Does NOT trip `--check-strict`.
- `upstream-missing` — disco emits but upstream registry doesn't list, and the type is not uncatalogued. Drift signal: alias-map typo, retired API, or scanner targeting obsolete type. `--check-strict` exits non-zero on any.

There is no synthetic bucket. Cross-tenant references (foreign account/sub/project) are modelled as **real self-node types** (`aws:iam:account`, `azure:microsoft.resources:subscriptions`, `gcp:cloudresourcemanager:project`), inserted as empty-attribute placeholders that version-populate when scanned — see store/CLAUDE.md "Reference-discovered placeholders". They bucket like any other real type.

## GCP Discovery quirks

- Fetch all versions of each relevant API (v1+v2 expose different collections, e.g. cloudbuild Trigger in v1, Connection in v2). Dedupe by upstream key.
- `singularize` strips trailing `s`/`ies` only — irregular plurals (Indexes→Index) need alias-map entry, not heuristic patches.
- Discovery resource collection name → singular → PascalCase. Walk recurses through nested `resources` tree.

## Per-provider upstream sources

The "upstream registry" a provider diffs against is not one fixed API:

- **AWS**: union of CloudFormation ListTypes (creds) ∪ the credential-free AWS Service Reference catalog (`internal/providers/aws/aws_servicereference.go`). Neither alone is complete — CFN omits SDK-real resources, SR omits CFN-modeled ones. `Fetch` appends both into one `[]UpstreamType`; `Build` dedupes case-insensitively. Detail: `internal/providers/aws/CLAUDE.md` §"Coverage upstream".
- **Azure**: ARM provider/resource-type list. **GCP**: Discovery documents (see quirks above).
