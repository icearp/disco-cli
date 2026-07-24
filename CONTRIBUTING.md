# Contributing to disco

Thanks for your interest in `disco`. This guide covers how to build, test, and
submit changes.

## Build

`disco` is a pure-Go binary. **`CGO_ENABLED=0` is mandatory** — the SQLite driver
is `modernc.org/sqlite` (a pure-Go transpile). Never swap it for `mattn/go-sqlite3`
or any other CGO dependency; doing so breaks the single-binary, no-C-toolchain
guarantee.

```bash
make build        # → ./disco, version-stamped from `git describe`
make all          # fmt + vet + test + build
make dist         # cross-compile linux/darwin/windows, amd64+arm64 → dist/
```

Plain `go build` works if you skip the Makefile, but the version stamp falls back
to a build-info revision (or `dev`):

```bash
CGO_ENABLED=0 go build -o disco .
```

## Test, vet, lint, format

```bash
CGO_ENABLED=0 go test ./...                     # full suite
CGO_ENABLED=0 go test ./store/... -run TestFoo -v   # a single test
go vet ./...
golangci-lint run --max-issues-per-linter 0 --max-same-issues 0
gofmt -w .                                      # run before every commit
make check-migrations                           # SQLite ↔ Postgres schema parity
```

Postgres-backed tests self-skip when Docker is unreachable, so the default
`go test ./...` runs cleanly without a database.

## Branch model

The primary branch is **`main`**. Fork your feature branch
from `main` and open your PR back into `main`.

## Conventions

- Keep it simple; don't reinvent the wheel; prefer human-readable code over clever
  code. Comment the non-obvious *why*, not the *what*.
- Match the style of the surrounding code (naming, comment density, idiom).
- Per-subtree `CLAUDE.md` files document local conventions — read the one for the
  area you're touching. `CODE_STRUCTURE.md` is the higher-level map.
- **Adding a provider, scanner, or resolver?** Start with
  `internal/providers/CLAUDE.md`, then the provider-specific guide
  (`internal/providers/{aws,azure,gcp}/CLAUDE.md`).
- New resource types are namespaced lowercase: `aws:ec2:instance`,
  `azure:compute:virtual-machine`, `gcp:compute:instance`.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md). Do not
open public issues for security problems.
