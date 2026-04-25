VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
BINARY     := bin/mcp-local-llm
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

.PHONY: build clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/mcp-local-llm/

clean:
	rm -rf bin/
