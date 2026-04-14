BINARY   := disco
DIST_DIR := dist
GO       := CGO_ENABLED=0 go

.PHONY: all deps test build clean

all: build

deps:
	go mod tidy

test:
	$(GO) test ./...

build:
	mkdir -p $(DIST_DIR)
	GOOS=linux   GOARCH=amd64  $(GO) build -o $(DIST_DIR)/$(BINARY)-linux-amd64 .
	GOOS=darwin  GOARCH=arm64  $(GO) build -o $(DIST_DIR)/$(BINARY)-darwin-arm64 .
	GOOS=windows GOARCH=amd64  $(GO) build -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe .

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
