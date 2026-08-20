BINARY  := rincewind
DIST    := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X github.com/vshn/rincewind/cmd.version=$(VERSION)"

# CGO is set per target, not once at the top: the binary must be static, and
# the race detector needs cgo. Setting it globally to 0 breaks `test` on
# linux — and only there, because darwin can link the race runtime without
# cgo, so the mistake passes locally and fails in CI.

.PHONY: all lint test build release clean

all: lint test build

# .golangci.yml is the definition of what counts as clean, and it covers
# formatting and vet as well, so there is nothing to run beside it. CI runs
# the same config through the golangci-lint action.
lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint missing; install with: brew install golangci-lint"; exit 1; }
	golangci-lint run

# -race because every script run is a goroutine.
test: export CGO_ENABLED = 1
test:
	go test -race ./...

# Static, as the tool ships.
build: export CGO_ENABLED = 0
build:
	go build $(LDFLAGS) -o $(BINARY) .

# What ships is defined once, in .goreleaser.yml — both operating systems and
# both architectures. This builds that matrix locally without publishing.
release:
	@command -v goreleaser >/dev/null || { echo "goreleaser missing; install with: brew install goreleaser"; exit 1; }
	goreleaser release --snapshot --clean

clean:
	rm -rf $(DIST) $(BINARY)
