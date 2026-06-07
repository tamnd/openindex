// Package open is the open-index and federation layer (architecture doc 10,
// implementation doc 11). It is the first differentiator: the index is a set of
// published, content-addressed, signed artifacts that a third party can download,
// load into an independent engine, and verify against a reproducible rebuild.
// Federation rides on the same artifacts, so a partition another operator hosts
// is just a signed leaf the root does not own.
//
// The discipline from doc 01 carries over: decentralization serves openness and
// trust and never costs the relevance and latency bar. So federation is gated by
// trust and bounded by the latency budget, and first-party serving always meets
// the contract on its own; the open layer adds reach and auditability on top.
//
// This package holds the artifact vocabulary and the content-addressing and
// signing that every open artifact shares. The exporters (open/ciff), the crowd
// signal (open/crowdsignal), and the federation gate (open/federation) build on
// it.
package open

import (
	"crypto/sha256"
	"fmt"

	"openindex"
)

// OperatorID names the operator that produced and signed an artifact. Provenance
// is per-operator because the federation trust gate and the poisoning audit both
// need to trace a shard back to who published it (doc 11.2, 11.5).
type OperatorID string

// ArtifactKind is the kind of open artifact. Each is produced from one consistent
// index snapshot so it is exactly what was served (doc 11.1).
type ArtifactKind uint8

const (
	// KindWARC is the raw crawl corpus, unchanged, so a citation resolves to an
	// archived record rather than a live URL.
	KindWARC ArtifactKind = iota
	// KindWAT is the WARC metadata derivative (JSON).
	KindWAT
	// KindWET is the extracted-plaintext derivative, about a sixth of the WARC.
	KindWET
	// KindCIFF is the lexical index in Common Index File Format, the lead
	// artifact: the smallest useful unit a third party can load and verify.
	KindCIFF
	// KindWebGraph is the link graph in WebGraph format, so PageRank-class
	// computations are independently reproducible.
	KindWebGraph
	// KindEmbeddings is the optional derived embedding dataset, published only
	// where licensing permits republication.
	KindEmbeddings
)

// String returns the canonical lowercase artifact-kind name used in manifests
// and log lines.
func (k ArtifactKind) String() string {
	switch k {
	case KindWARC:
		return "warc"
	case KindWAT:
		return "wat"
	case KindWET:
		return "wet"
	case KindCIFF:
		return "ciff"
	case KindWebGraph:
		return "webgraph"
	case KindEmbeddings:
		return "embeddings"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// Artifact is a published unit: a kind, the snapshot it was produced from, the
// content address of its bytes, the operator that produced it, and that
// operator's signature over the rest. The content address lets two parties
// confirm they hold the same bytes; the signature lets a consumer verify
// integrity and provenance before trusting it (doc 11.2). The bytes themselves
// are not held here, only their address, because an artifact is a manifest entry
// and the bytes move over the cold-tier transport (doc 11.2).
type Artifact struct {
	Kind     ArtifactKind
	Snapshot openindex.SnapshotID
	Content  openindex.ContentHash
	Operator OperatorID
	Sig      []byte
}

// Address is the content address of a byte slice: its SHA-256 digest. Two parties
// that compute Address over the same bytes get the same ContentHash, which is the
// whole point of content-addressing an artifact (doc 11.2).
func Address(data []byte) openindex.ContentHash {
	return sha256.Sum256(data)
}

// SigningBytes is the canonical byte string an operator signs and a consumer
// verifies: the artifact's identity (kind, snapshot, content address) without the
// signature itself. Signing the content address rather than the bytes keeps the
// signature small and lets a consumer verify provenance from the manifest before
// it has fetched the bytes, then confirm the bytes against the address
// separately.
func (a Artifact) SigningBytes() []byte {
	out := make([]byte, 0, 1+len(a.Snapshot)+len(a.Content)+len(a.Operator))
	out = append(out, byte(a.Kind))
	out = append(out, a.Snapshot...)
	out = append(out, a.Content[:]...)
	out = append(out, a.Operator...)
	return out
}
