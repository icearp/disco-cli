BINARY   := disco
DIST_DIR := dist
GO       := CGO_ENABLED=0
TAGS     ?=
TAGFLAG  := $(if $(TAGS),-tags "$(TAGS)",)
VERSION  ?= $(shell git describe --tags --always --dirty=+dirty 2>/dev/null || echo dev)
LDFLAGS  := -X 'codeberg.org/icearp/disco/cmd.Version=$(VERSION)'

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
	$(GO) go build $(TAGFLAG) -ldflags "$(LDFLAGS)" -o $(BINARY) .

build-paid:
	$(MAKE) build TAGS=paid

oss-sync:
	./scripts/oss-sync.sh

dist:
	$(GO) GOOS=linux   GOARCH=amd64  go build -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64 . && upx --best --lzma $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-amd64
	$(GO) GOOS=linux   GOARCH=arm64  go build -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-arm64 . && upx --best --lzma $(DIST_DIR)/$(BINARY)-$(VERSION)-linux-arm64
	$(GO) GOOS=darwin  GOARCH=arm64  go build -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64 . && tar -cJf $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.tar.xz $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64 && rm $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64
	$(GO) GOOS=darwin  GOARCH=amd64  go build -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64 . && tar -cJf $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64.tar.xz $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64 && rm $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64
	$(GO) GOOS=windows GOARCH=amd64  go build -ldflags "$(LDFLAGS) -w -s" -trimpath -o $(DIST_DIR)/$(BINARY)-$(VERSION)-windows-amd64.exe . && upx --best --lzma $(DIST_DIR)/$(BINARY)-$(VERSION)-windows-amd64.exe

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
