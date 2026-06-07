# OpenIndex

A web-scale search engine, built from scratch in Go, whose index is open.

Only four organizations on Earth run a real general web index: Google, Microsoft,
Yandex, Baidu. Everyone else rents. That arrangement just broke. The Bing Web Search
API returned `410 Gone` on August 11, 2025 and took the entire tier of products that
resold it down with it. In the same window a US federal court ruled Google an illegal
monopolist in general search and ordered it to share parts of its index with
"qualified competitors." The moat is being pried open at the exact moment the
dependency tier is forced to find a new supplier.

OpenIndex is the bet that the next general index should be the open one. The crawled
corpus, the link graph, and the inverted index are published as immutable,
content-addressed, signed, openly licensed artifacts that anyone can download, verify,
and build on. The index is infrastructure, not a trade secret.

> **Status:** early construction. The architecture is fully specified and the engine
> is being built subsystem by subsystem, each behind its own reviewed pull request.
> This is a moonshot with a buildable path, not a product you can query yet. See the
> [roadmap](#roadmap).

## Why this exists

The economics of a from-scratch index have been irrational for twenty-five years, so
nobody tried. Three things changed at once, and the window they open is real but
time-boxed:

- **The dependency tier collapsed.** Bing's API is gone; its replacement returns
  summaries, not results. Brave is currently the only independent, at-scale Western
  search API. A near-monopoly on independent supply is a near-monopoly begging for a
  second entrant.
- **The moat is being legally opened.** *United States v. Google* set the direction:
  for the first time there is a potential legal on-ramp to the data flywheel that made
  every prior entrant fail at cold start.
- **Demand moved from links to answers**, and the answer leaders are built on
  contested, opaquely crawled foundations. An answer engine on a cleanly crawled,
  consent-respecting, auditable index is a provenance story none of the incumbents
  can tell.

## What OpenIndex optimizes for, in priority order

1. **An open, public, auditable index.** When a design choice trades ranking quality
   against the auditability or reusability of the index, the default is openness and
   the closed optimization becomes an opt-in serving-side layer. We cannot out-spend
   Google's data flywheel, so we compete on the one axis it structurally cannot match.
2. **AI-native answers, grounded and cited.** Every generated claim is tied to a
   character span in a source document, verified by an entailment check, and traceable.
   The engine answers what it can prove and says so when it cannot.
3. **Decentralization where it actually works.** The expensive, embarrassingly parallel
   work, crawl fetch and raw-shard storage, federates across volunteer and partner
   operators. The authoritative index and the serving path stay centralized or
   federated among vetted operators, because every fully-P2P search engine to date
   (YaCy, FAROO, Presearch) traded away the latency and spam-resistance that make
   search usable.

## The prime directive

Three contracts are non-negotiable; everything else is subordinate to them.

- **The open-index contract.** The corpus, graph, and index are published as immutable,
  signed, content-addressed, openly licensed artifacts with named snapshots and
  reproducible pipelines. Re-run a stage against the same input, get the same bytes.
- **The provenance contract.** Every crawled page records how, when, and under what
  consent it was fetched; OpenIndex crawls politely and verifiably and never ships an
  answer it cannot attribute.
- **The relevance and latency bar.** Every release clears its NDCG@10 / MRR@10
  relevance gate and its P99 latency budget on reference hardware, or the feature is
  redesigned, gated, or deferred.

## Architecture

OpenIndex is a pipeline read bottom to top, plus a control plane and a serving tree
that cut across it.

```
   Users / API clients ── Serving tree (root → aggregator → leaf scatter/gather)
                                │                    │
                         ┌──────┴──────┐      ┌──────┴───────┐
       Answer engine ◄── │  Ranking & retrieval: BM25F + dense + LTR cascade │
       RAG + cite        └──────┬──────┘      └──────┬───────┘
                         ┌──────┴──────┐      ┌──────┴───────┐
                         │ Inverted    │      │ Vector /     │
                         │ index       │      │ semantic idx │
                         └──────┬──────┘      └──────┬───────┘
                         ┌──────┴─────────────────────┴──────┐
                         │ Indexing pipeline (batch + incr.)  │
                         └──────┬─────────────────────────────┘
                  ┌────────────┴────────────┐
                  │ Content store / WebTable │   Link graph (WebGraph)
                  └────────────┬────────────┘
                         ┌──────┴──────┐
                         │   Crawler   │  frontier → fetch → parse → WARC
                         └─────────────┘

   Cross-cutting: open-index publication & federation · control plane ·
                  observability, eval harness, spam/Sybil defense
```

The data plane flows up. The crawler writes WARC and emits parsed documents and
extracted links. The content store and link graph hold them. The indexing pipeline
turns documents into inverted-index segments and embedding vectors, incrementally for
freshness, with periodic batch rebuilds. Ranking composes the lexical and vector
stores into a cascade, the serving tree fans queries out to leaves and merges, and the
answer engine layers grounded synthesis on top. The open-index subsystem taps the data
plane at three points (corpus, graph, index) and publishes them as artifacts.

## Why Go

A crawler is a bounded producer–consumer system over millions of host-politeness
queues; a serving root is a scatter-gather over thousands of shard leaves with strict
per-shard deadlines. Goroutines, channels, `context` deadlines, and `errgroup` fit both
exactly. The fleet is one module compiled to one static binary per role. Pure-Go LSM
engines (Pebble, Badger) let us own the storage path without cgo. gRPC over protobuf is
the native internal data plane.

Where Go is weak the spec routes around it: ranking models, embedding inference, and
the answer LLM are not Go and are reached over gRPC or a narrow, batched, profiled cgo
boundary. **Go owns orchestration and the data plane; native code owns the math.**

## Repository layout

```
openindex/
  crawler/    frontier, fetcher, robots, dedup, render, WARC
  storage/    WebTable, fs client, link graph, codecs
  index/      segment, FST term dictionary, postings, merge, forward store
  vector/     embedding client, ANN (HNSW/DiskANN), quantization
  rank/       BM25F, PageRank, LTR client, fusion, query understanding
  serve/      serving tree (root/aggregator/leaf), tail latency, cache, mixer
  answer/     RAG pipeline, grounding, citation verify, router, LLM client
  control/    shard maps, health, config, placement
  open/       CIFF export, signing, content-addressing, federation, crowd signal
  telemetry/  OpenTelemetry setup, metrics, tracing helpers
  proto/      generated gRPC services + messages (the data plane contract)
  cmd/        one thin main per role
```

One package per subsystem, a strict acyclic dependency direction, no `internal/`
(the open-ecosystem goal wants importable packages), and no `/vN` suffix.

## Building

Requires Go (current stable) and, to regenerate the data plane, `protoc` with the
`protoc-gen-go` / `protoc-gen-go-grpc` plugins.

```sh
make build   # compile every cmd/ role into ./bin
make test    # unit, property, and golden-file tests
make race    # the suite under the race detector
make lint    # gofmt + go vet + golangci-lint
make proto   # regenerate proto/*.pb.go from proto/openindex.proto
```

`CGO_ENABLED=0` is the default so every binary is fully static and cross-compilable;
the one package that may bind native ANN code carries its own build tag.

## Roadmap

The build order runs from a single-cluster MVP bootstrapped on Common Crawl up to the
Google-scale target, each milestone gated on a literal relevance and latency
acceptance test. The cold-start flywheel is attacked at three points: bootstrap the
corpus on Common Crawl, generate behavioral signal with privacy-preserving
crowd-sourcing, and qualify for the *US v. Google* data-sharing remedy. The full
milestone map, cost model, and risk register live in the specification.

## Specification

The complete architecture and implementation specification covers vision, market
analysis, and a document per subsystem with the algorithms, formats, and trade-offs
spelled out as Architecture Decision Records. It is maintained alongside the code and
is the source of truth for every design choice here.

## License

Apache License 2.0. See [LICENSE](LICENSE). The openness of the code is the point: the
index it produces is meant to be rebuilt, audited, and reused.
