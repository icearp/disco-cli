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
LDFLAGS  := -X 'codeberg.org/icearp/disco/cmd.Version=$(VERSION)'
# syft generates the release SBOMs (CycloneDX + SPDX). Pinned; keep in sync with
# SYFT_VERSION in .forgejo/workflows/release.yaml. Invoked via `go run …@version`
# so it never enters disco's go.mod/go.sum.
SYFT_VERSION ?= v1.49.0

.PHONY: all deps fmt lint vet test build check-migrations clean dist sbom

check-migrations:
	./scripts/check-migrations.sh

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

dist:
	$(GO) GOAMD64=v3 GOOS=linux   GOARCH=amd64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64 . && upx --best --lzma $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64
	$(GO) GOOS=linux   GOARCH=arm64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-arm64 . && upx --best --lzma $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-arm64
	$(GO) GOOS=darwin  GOARCH=arm64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64 . && tar -cJf $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.tar.xz $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64 && rm $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64
	$(GO) GOOS=darwin  GOARCH=amd64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64 . && tar -cJf $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64.tar.xz $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64 && rm $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64
	$(GO) GOAMD64=v3 GOOS=windows GOARCH=amd64  go build -tags grpcnotrace -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-windows-amd64.exe

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
