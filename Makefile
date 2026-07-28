BINARY   := disco
DIST_DIR := dist
GO       := CGO_ENABLED=0
# TAGS opts into a provider subset for slim, provider-specific builds, e.g.
# `make build TAGS="slim aws"` compiles aws only (azure/gcp SDKs not linked).
# Bare `make build` compiles every provider. See internal/providers/all.
#
# grpcnotrace is always on: google.golang.org/grpc (pulled in via the GCP SDK)
# imports golang.org/x/net/trace, whose init() unconditionally registers
# /debug/requests + /debug/events HTTP handlers backed by html/template ->
# text/template -> reflect.MethodByName. That trips the linker's whole-binary
# DCE kill switch (see CLAUDE.md "text/template defeats linker DCE") the same
# way OPA's now-fixed gojsonschema formatter used to. grpc ships the
# `grpcnotrace` build tag precisely to strip that path; drop the tag and the
# binary balloons regardless of the OPA fix.
TAGS     ?=
TAGFLAG  := -tags "grpcnotrace$(if $(TAGS), $(TAGS),)"
VERSION  ?= $(shell git describe --tags --always --dirty=+dirty 2>/dev/null || echo dev)
LDFLAGS  := -X 'github.com/icearp/disco-cli/cmd.Version=$(VERSION)'
# syft generates the release SBOMs (CycloneDX + SPDX). Pinned; keep in sync with
# SYFT_VERSION in .github/workflows/release.yaml. Invoked via `go run …@version`
# so it never enters disco's go.mod/go.sum.
SYFT_VERSION ?= v1.49.0
# govulncheck gates releases on known-vulnerable, reachable deps. Pinned; keep in
# sync with GOVULNCHECK_VERSION in .github/workflows/release.yaml. Invoked via
# `go run …@version` so it never enters disco's go.mod/go.sum. The vuln DB
# (vuln.go.dev) is queried live at run time — the pin fixes the tool, not the data.
GOVULNCHECK_VERSION ?= v1.6.0

.PHONY: all deps fmt lint vet test build check-migrations gen-regions clean dist sbom vulncheck

check-migrations:
	./scripts/check-migrations.sh

# gen-regions rebuilds the per-service region table the AWS scanner uses to skip
# (service x region) pairs AWS does not offer. Output is committed; there is no
# matching check target because regionsgen's own test is what gates staleness in
# CI, which runs go test and no make targets.
gen-regions:
	go generate ./internal/providers/aws/awsregions/...

all: fmt vet test build

deps:
	go mod tidy

fmt:
	gofmt -w -s .

lint:
	golangci-lint run ./...

vet:
	go vet ./...

test:
	$(GO) go test $(TAGFLAG) ./...

build:
	$(GO) go build $(TAGFLAG) -ldflags "$(LDFLAGS)" -o $(BINARY) .

# sbom scans the freshly built (uncompressed) binary and emits both a CycloneDX
# and an SPDX SBOM from the binary's Go buildinfo. Scan the RAW binary (before
# the dist/CI upx/xz steps) so the SBOM derives straight from the Go build and
# doesn't lean on syft's ability to see through compression. The linked module
# set is GOOS/GOARCH-independent, so one local scan documents the dep graph; CI
# emits a per-artifact SBOM per target.
sbom: build
	mkdir -p $(DIST_DIR)
	go run github.com/anchore/syft/cmd/syft@$(SYFT_VERSION) scan $(BINARY) \
	  -o cyclonedx-json@1.7=$(DIST_DIR)/$(BINARY)-$(VERSION).cdx.json \
	  -o spdx-json@2.3=$(DIST_DIR)/$(BINARY)-$(VERSION).spdx.json

# vulncheck runs govulncheck binary-mode symbol reachability over the binary the
# `build` target just produced — same config disco ships ($(TAGFLAG) carries
# grpcnotrace, $(GO) sets CGO off). Exits non-zero on a reachable vuln; mirrors
# the release-gate step in CI.
#
# Source mode (`govulncheck ./...`) is NOT an option: it builds whole-program SSA
# over disco's ~496-module graph (three full cloud SDKs) and needs >23GB, OOMing
# a 31GB workstation. Binary mode reads the symbol table instead — still
# symbol-level, not module-level, and fits in <8GB.
#
# Depends on `build`, not `dist`: $(LDFLAGS) deliberately omits -w -s, and `-s`
# would strip the symbol table that binary mode needs. The dist/CI release builds
# do strip; that only removes debug data, not code, so reachability computed on
# the unstripped binary holds for the shipped one.
#
# The `go tool nm` guard is not paranoia: govulncheck does NOT error on a
# stripped binary. It silently drops to module granularity and reports whole
# modules as reachable — still under a "Symbol Results" header — so a stripped
# scan yields spurious failures rather than an obvious one. Fail legibly instead.
vulncheck: build
	@go tool nm $(BINARY) >/dev/null 2>&1 || { echo "$(BINARY) has no symbol table — did -s/-w reach LDFLAGS?" >&2; exit 1; }
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) -mode binary $(BINARY)

dist:
	$(GO) GOAMD64=v3 GOOS=linux   GOARCH=amd64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64 . && upx --best --lzma $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64
	$(GO) GOOS=linux   GOARCH=arm64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-arm64 . && upx --best --lzma $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-arm64
	$(GO) GOOS=darwin  GOARCH=arm64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64 . && tar -cJf $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.tar.xz $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64 && rm $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64
	$(GO) GOOS=darwin  GOARCH=amd64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64 . && tar -cJf $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64.tar.xz $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64 && rm $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64
	$(GO) GOAMD64=v3 GOOS=windows GOARCH=amd64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-windows-amd64.exe

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
