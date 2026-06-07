# OpenIndex build entrypoints. The fleet is one Go module built into one static
# binary per role (impl spec 01.7), so the targets here are deliberately thin.

GO        ?= go
GOFLAGS   ?=
# CGO is off by default: the whole fleet is fully static and cross-compilable.
# The one package that may bind native ANN code (vector/, impl spec 06) carries
# its own build tag and is the documented exception.
export CGO_ENABLED ?= 0

ROLES := crawler indexer leaf aggregator root mixer answer publisher

.PHONY: all build test race lint vet fmt tidy proto cover clean $(ROLES)

all: build

## build: compile every cmd/ role into ./bin
build:
	@mkdir -p bin
	@for r in $(ROLES); do \
		if [ -d "cmd/$$r" ]; then \
			echo "  build $$r"; \
			$(GO) build $(GOFLAGS) -o bin/$$r ./cmd/$$r || exit 1; \
		fi; \
	done

## test: run the unit, property, and golden-file tests
test:
	$(GO) test ./...

## race: run the suite under the race detector (CGO is forced on for -race)
race:
	CGO_ENABLED=1 $(GO) test -race ./...

## cover: produce a coverage profile and print the per-package summary
cover:
	$(GO) test -coverprofile=cover.out ./...
	$(GO) tool cover -func=cover.out | tail -1

## vet: go vet across the module
vet:
	$(GO) vet ./...

## lint: golangci-lint (config in .golangci.yml)
lint:
	golangci-lint run ./...

## fmt: gofmt the tree
fmt:
	gofmt -s -w .

## tidy: prune and verify go.mod / go.sum
tidy:
	$(GO) mod tidy

## proto: regenerate the gRPC data plane from proto/*.proto
proto:
	protoc --go_out=. --go_opt=module=openindex \
	       --go-grpc_out=. --go-grpc_opt=module=openindex \
	       proto/openindex.proto

## clean: remove build and coverage output
clean:
	rm -rf bin dist cover.out
