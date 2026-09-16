# CLAUDE.md — `internal/sdkinv/`

SDK source cache + extractors that derive the coverage denominator. Imports nothing from
`internal/providers` or `internal/coverage`. Extractors register from `init()`; blank imports
live in `internal/sdkinv/all` (no slim build tags — extractors link no cloud SDK).

## Cache layout (`$XDG_CACHE_HOME/disco/sdk/<provider>@<ref>/`, `disco coverage sdk fetch|status`, `make sdk-fetch`)

- `manifest.json` written last; a dir without it is "absent". Fetch lands in `.tmp-*` then renames.
- `aws@<tag>/repo/codegen/sdk-codegen/aws-models/*.json` (431 Smithy models) +
  `service-reference/index.json` and `<service>.json` (456 docs).
- `azure@<sha>/repo/sdk/resourcemanager/<rp>/arm<rp>/{*_client.go,models.go,response_types.go,responses.go}`.
  Filter anchors on `sdk/resourcemanager/` — the monorepo also ships `profile/*/resourcemanager` copies.
  Monorepo HEAD holds one major per module dir (majors are tags), so no version picking.
- `gcp@<ver>/api/<api>/<ver>/<api>-api.json` (654 Discovery docs), copied from GOMODCACHE when
  present, else the module zip from proxy.golang.org.
- Full fetch ≈ 26 s / 435 MB. Rerun is a no-op; `--force` refetches.

## Pins (`pins.go`)

- `AWSSDKRef` = aws-sdk-go-v2 `release-YYYY-MM-DD` tag; `AzureSDKRef` = commit SHA (no monorepo tag).
- `GCPAPIRef` is a **fallback** for binaries that do not link `google.golang.org/api`; the disco
  binary reads the version from `debug.ReadBuildInfo()`. `TestPinMatchesGoMod` fails when go.mod
  moves — bump the pin in the same commit as the dependency.
- A pin bump changes the denominator; reports print the pins.

## Archive handling (`fetch.go`)

- GitHub tarballs prefix `<repo>-<ref>/` (Strip 1); module zips `<module>@<ver>/` (Strip 2).
- Entry paths are backslash-normalised, `..`/absolute rejected, 64 MB per-entry cap; JSON-index
  entry names must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`.

## Catalog facts (verified 2026-09-16)

- Smithy service shape: `aws.auth#sigv4.name` equals the Service Reference service name (join key);
  `aws.api#service` carries `sdkId`/`arnNamespace`/`cloudFormationName`. Ops carry
  `smithy.api#required` on input members and `smithy.api#paginated`. Resource shapes are rare
  (lambda 11, ec2/s3/rds 0) — never rely on them.
- Service Reference doc: `Actions[].Annotations.Properties.{IsList,IsWrite}`, `Actions[].Resources[]`
  (targets), `Operations[].AuthorizedActions` (SDK op → IAM action, e.g. `ListObjectsV2`→`ListBucket`),
  `Resources[].ARNFormats`.
