# Release pipeline: maintainer notes

How a tagged release gets its SBOMs and its vulnerability gate, and why each step is shaped the
way it is. The user-facing summary is in the README ("Supply-chain SBOMs", "Vulnerability gating
on release"). The rules that must not break are repeated in the root `CLAUDE.md` under "Release
pipeline".

## SBOMs

- Each tagged release attaches a CycloneDX (`.cdx.json`) and an SPDX (`.spdx.json`) SBOM for every
  binary, beside its `.sha256` sidecar. `syft` generates them from the binary's Go buildinfo.
  `make sbom` reproduces them locally into `dist/`.
- They are generated from the **raw binary before upx/xz**, because `go version -m` cannot read a
  upx-packed binary. Both the Makefile target and the CI step (`.github/workflows/release.yaml`)
  run before the compression steps.
- `SYFT_VERSION` is pinned in both places; keep them in sync. The emitted spec versions are pinned
  in the `-o` selectors (`cyclonedx-json@1.7`, `spdx-json@2.3`), so a syft bump cannot silently
  reshape the output.
- syft runs via `go run …@version` and is never added to disco's `go.mod`.
- The SBOM is not yet embedded in the `disco snapshot` evidence archive. That is a signed-SBOM
  follow-up.

## Vulnerability gate

- The CI `test` job runs `govulncheck` against the shipped build config (`-tags grpcnotrace`,
  `CGO_ENABLED=0`). A **reachable** known vulnerability exits non-zero and fails `test`, so
  `build` and `release` never run (`build: needs: test`).
- It runs once per tag. The vulnerability DB (vuln.go.dev) is queried live, so pinning
  `GOVULNCHECK_VERSION` fixes the tool, not the data. Keep the pin in sync between the `Makefile`
  and `.github/workflows/release.yaml`. `make vulncheck` mirrors the CI step locally.
- The tool runs via `go run …@version` and is never in `go.mod`.
- It gates releases only. A continuous push/PR/cron workflow, which would catch vulnerabilities
  disclosed between tags, is a deliberate follow-up.

### Binary mode, not source mode

`govulncheck -mode binary` scans a freshly built binary. Source mode builds whole-program SSA over
three cloud SDKs and needs **more than 23 GB** of memory. Binary mode reads the symbol table, still
gives symbol-level reachability, and fits in under 8 GB.

### Never scan a stripped binary

A `-w -s` stripped binary has no symbol table. govulncheck does not error on that: it silently
falls back to module granularity and reports whole modules reachable. The result is a **spurious
red** (for example `x/crypto/openpgp`) that someone would paper over with `continue-on-error`.

Both `make vulncheck` (which depends on `build`, not `dist`) and the CI step (a throwaway
`vulnscan-target`) first assert that `go tool nm <binary>` succeeds. Stripping removes debug data,
not code, so the result for the unstripped binary holds for the shipped one.

### The `go` directive is a security floor

Most reachable findings are standard-library vulnerabilities fixed by a toolchain patch release.
When the gate goes red on `Standard library` entries, the fix is to bump the `go` directive in
`go.mod`.

**Bump the Dockerfile's `GO_VERSION` with it.** The golang images set `GOTOOLCHAIN=local`, so an
older builder refuses `go mod download`. A floating tag on a discontinued alpine variant also
stays frozen.
