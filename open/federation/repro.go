// Reproducible-build verification (implementation doc 11.4). Because the corpus,
// the link graph, and the index are all published, a third party can rebuild the
// index from the corpus and confirm it matches the signed snapshot. This is the
// auditability half of the open contract and the strongest defense against a
// malicious operator: a shard whose bytes do not match an independent rebuild is
// rejected no matter who signed it (doc 11.5). It is why doc 01.7 makes
// reproducible builds a hard requirement, so the rebuild is deterministic.

package federation

import (
	"errors"

	"openindex/open"
)

// ErrRebuildMismatch is returned when a rebuilt artifact's content address does
// not match the signed artifact's. The signature may be perfectly valid and the
// operator known; the bytes still do not reproduce, so the artifact is rejected.
var ErrRebuildMismatch = errors.New("federation: rebuilt artifact does not match the signed content address")

// VerifyRebuild is the audit. It checks two things in order: the signed artifact
// verifies under its operator's registered key (provenance and integrity, doc
// 11.2), and the independently rebuilt bytes content-address to the same hash the
// artifact claims (reproducibility). Only when both hold is the artifact trusted.
// A valid signature over bytes that do not reproduce still fails, because a
// signature proves who published an artifact, not that the artifact is honest.
func VerifyRebuild(reg *open.Registry, signed open.Artifact, rebuilt []byte) error {
	if err := reg.VerifyArtifact(signed); err != nil {
		return err
	}
	if open.Address(rebuilt) != signed.Content {
		return ErrRebuildMismatch
	}
	return nil
}
