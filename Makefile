BINARY   := disco
DIST_DIR := dist
CGO      := CGO_ENABLED=0

.PHONY: all fmt lint vet test build clean dist

all: fmt vet test build

fmt:
	gofmt -w .

lint:
	golangci-lint run ./...

vet:
	go vet ./...

test:
	$(CGO) go test ./...

build:
	$(CGO) go build -o $(BINARY) .

dist:
	$(CGO) GOOS=linux   GOARCH=amd64  go build -o $(DIST_DIR)/disco-linux-amd64 .
	$(CGO) GOOS=darwin  GOARCH=arm64  go build -o $(DIST_DIR)/disco-darwin-arm64 .
	$(CGO) GOOS=windows GOARCH=amd64  go build -o $(DIST_DIR)/disco-windows-amd64.exe .

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
