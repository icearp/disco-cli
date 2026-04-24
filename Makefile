BINARY   := disco
DIST_DIR := dist
GO       := CGO_ENABLED=0

.PHONY: all deps fmt lint vet test build clean dist

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
	$(GO) go test ./...

build:
	$(GO) go build -o $(BINARY) .

dist:
	mkdir -p $(DIST_DIR)
	$(GO) GOOS=linux   GOARCH=amd64  go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY)-linux-amd64 . && upx -9 $(DIST_DIR)/$(BINARY)-linux-amd64
	$(GO) GOOS=darwin  GOARCH=arm64  go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY)-darwin-arm64 .
	$(GO) GOOS=windows GOARCH=amd64  go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe . && upx -9 $(DIST_DIR)/$(BINARY)-windows-amd64.exe

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
