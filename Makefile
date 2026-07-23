BIN_DIR    := bin
BINARY     := $(BIN_DIR)/moonshine
LIB_DIR    := .moonshine/lib
MOONSHINE_SRC ?=

export CGO_ENABLED ?= 1

# CLI's own build version (see cmd/moonshine/version.go) -- semver tag if
# HEAD is tagged, else an abbreviated commit hash; "-dirty" suffix if the
# worktree has uncommitted changes. Falls back to "dev" outside a git
# checkout (e.g. building from a source tarball with no .git directory).
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build buildlib clean test smoke fmt vet proto

all: build

## build: Build the moonshine CLI into ./bin.
build: $(BIN_DIR)
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/moonshine

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

## buildlib: Build libmoonshine from a local moonshine checkout into .moonshine/lib.
##           Usage: make buildlib MOONSHINE_SRC=~/projects/github/moonshine
##           (or export MOONSHINE_SRC in your shell instead of passing it here).
buildlib:
	./scripts/build-libmoonshine.sh $(MOONSHINE_SRC)

## test: Run the regular (non-native) Go test suite.
test:
	go test ./...

## smoke: Run the tests that exercise a real built libmoonshine (see internal/moonshine/smoke_test.go).
smoke:
	MOONSHINE_LIB_DIR=$(CURDIR)/$(LIB_DIR) go test -tags moonshinesmoke ./internal/moonshine/... -v

## proto: Regenerate internal/serve/servepb from internal/serve/serve.proto.
##        Requires protoc, protoc-gen-go, and protoc-gen-go-grpc on PATH
##        (e.g. `brew install protobuf && go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
##        google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`).
proto:
	protoc --go_out=. --go_opt=module=github.com/ghchinoy/moonshine-go \
		--go-grpc_out=. --go-grpc_opt=module=github.com/ghchinoy/moonshine-go \
		internal/serve/serve.proto

fmt:
	gofmt -l .

vet:
	go vet ./...

## clean: Remove build output (leaves .moonshine/lib alone; see `make distclean`).
clean:
	rm -rf $(BIN_DIR)

## distclean: Also remove the staged native library output.
distclean: clean
	rm -rf .moonshine
