BINARY   := disco
DIST_DIR := dist
GO       := CGO_ENABLED=0
TAGS     ?=
TAGFLAG  := $(if $(TAGS),-tags "$(TAGS)",)

.PHONY: all deps fmt lint vet test test-paid build build-paid clean dist oss-sync

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

test-paid:
	$(MAKE) test TAGS=paid

build:
	$(GO) go build $(TAGFLAG) -o $(BINARY) .

build-paid:
	$(MAKE) build TAGS=paid

oss-sync:
	./scripts/oss-sync.sh

dist:
	$(GO) GOOS=linux   GOARCH=amd64  go build -ldflags "-w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-linux-amd64 . && upx -9 $(DIST_DIR)/$(BINARY)-linux-amd64
	$(GO) GOOS=darwin  GOARCH=arm64  go build -ldflags "-w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-darwin-arm64 .
	$(GO) GOOS=windows GOARCH=amd64  go build -ldflags "-w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe . && upx -9 $(DIST_DIR)/$(BINARY)-windows-amd64.exe

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
