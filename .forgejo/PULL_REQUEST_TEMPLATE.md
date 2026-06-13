## What this changes

A short description of the change and why.

## Checklist

- [ ] Branched from `dev` and targeting `dev`
- [ ] `CGO_ENABLED=0 go test ./...` passes
- [ ] `go vet ./...` and `golangci-lint run` are clean
- [ ] `gofmt -w .` run (no formatting diff)
- [ ] `make check-migrations` passes (if any schema/migration changed)
- [ ] New scanners/resolvers follow `internal/providers/CLAUDE.md` and have tests
- [ ] No CGO dependency introduced (pure-Go only)

## Notes for reviewers

Anything reviewers should focus on, follow-ups intentionally deferred, etc.
