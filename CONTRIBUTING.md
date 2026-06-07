# Contributing to OpenIndex

OpenIndex is built subsystem by subsystem, each landing as its own reviewed pull
request against `main`. This document is the short version of the rules the code
follows; the long version lives in the implementation specification.

## Ground rules

- **One module, static binaries.** `CGO_ENABLED=0` is the default. The only package
  permitted to use cgo is `vector/` (the ANN binding), behind a build tag, so the rest
  of the fleet stays fully static and cross-compilable.
- **Dependency direction is layered and acyclic.** A package may import only lower
  layers plus the `proto/` and `telemetry/` leaves. `cmd/` mains are thin: flags,
  telemetry init, config load, and a call into a library. No business logic in `main`.
- **Errors are values, wrapped with context** (`fmt.Errorf("...: %w", err)`). No
  package panics across its own boundary. Context and deadlines are propagated
  everywhere and cancelled with `defer cancel()` at the creation site.
- **Bounded concurrency.** `errgroup` with `SetLimit` for fan-out; worker pools with
  admission control, never unbounded `go`. `singleflight` on stampede-prone cache
  misses.
- **The wire type is not the domain type.** Convert protobuf messages to package-owned
  Go structs at the package edge.

## Before you push

```sh
make fmt    # gofmt -s
make vet
make lint
make test
make race   # for anything touching shared state or the serving path
```

CI runs build, test, the race detector, `gofmt`/`vet`/`golangci-lint`, a `go mod tidy`
check, and a check that the generated proto code is up to date. All must be green.

## Commits and pull requests

- Small, focused commits with a clear imperative subject and a body that explains the
  *why*, in a human voice. One logical change per commit.
- A pull request implements one subsystem or one coherent slice of one. Reference the
  relevant specification document in the description so a reviewer can check the code
  against its source of truth.
- New on-disk or wire formats ship with golden-file tests; codecs and merge logic ship
  with round-trip property tests.

## Tests

Every codec, scorer, and data structure has unit tests. The relevance harness
(NDCG@10 / MRR@10 / Recall@k) is itself a test and gates changes to the ranking path:
a regression past threshold fails the build. That is how the relevance bar is enforced
mechanically rather than by reviewer vigilance.
