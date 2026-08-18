# Static binaries, as the tool ships. See docs/platform.md.
BINARY := aldediggi
DIST   := dist
export CGO_ENABLED = 0

.PHONY: all lint test build build-macos build-linux release inventory clean

all: lint test build

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint missing; install with: brew install golangci-lint"; exit 1; }
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi
	go vet ./...
	golangci-lint run

test:
	go test ./...

build:
	go build -o $(BINARY) .

build-macos:
	GOOS=darwin GOARCH=arm64 go build -trimpath -o $(DIST)/$(BINARY)-darwin-arm64 .
	GOOS=darwin GOARCH=amd64 go build -trimpath -o $(DIST)/$(BINARY)-darwin-amd64 .

build-linux:
	GOOS=linux GOARCH=amd64 go build -trimpath -o $(DIST)/$(BINARY)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -trimpath -o $(DIST)/$(BINARY)-linux-arm64 .

release: build-macos build-linux

# docs/inventory.md is generated from the registry; the test fails while stale.
inventory:
	go test ./cmd -run TestInventory -update

clean:
	rm -rf $(DIST) $(BINARY)
