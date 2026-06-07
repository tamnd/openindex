// Artifact signing and the operator key registry (doc 11.2 and 11.5). Every
// published artifact is signed by its producing operator so a consumer verifies
// integrity and provenance before serving or trusting it, and a poisoned shard
// is traceable to the operator that signed it. Signing uses ed25519: small
// signatures and keys, fast verification, and no parameter choices to get wrong.

package open

import (
	"crypto/ed25519"
	"errors"
)

// ErrUnknownOperator is returned when an artifact names an operator the registry
// does not hold a key for. An unknown operator is not trusted, so its artifacts
// are rejected rather than verified against a guessed key.
var ErrUnknownOperator = errors.New("open: unknown operator")

// ErrBadSignature is returned when an artifact's signature does not verify under
// its operator's public key, which means the bytes or the provenance were
// tampered with.
var ErrBadSignature = errors.New("open: signature does not verify")

// Sign produces the operator signature over an artifact's signing bytes. The
// caller sets the signature on the artifact. The private key never leaves the
// producing operator.
func Sign(a Artifact, priv ed25519.PrivateKey) []byte {
	return ed25519.Sign(priv, a.SigningBytes())
}

// Verify checks an artifact's signature against a public key. It reports nil when
// the signature is valid for exactly these bytes and this provenance.
func Verify(a Artifact, pub ed25519.PublicKey) error {
	if !ed25519.Verify(pub, a.SigningBytes(), a.Sig) {
		return ErrBadSignature
	}
	return nil
}

// Registry maps an operator to its published public key. It is the trust anchor:
// the federation gate (doc 11.4) and the artifact consumer both verify against
// the key the registry holds, and an operator the registry does not know is not
// trusted. The registry is small and changes rarely, so it is the kind of state
// the control plane (doc 10) distributes.
type Registry struct {
	keys map[OperatorID]ed25519.PublicKey
}

// NewRegistry returns an empty operator-key registry.
func NewRegistry() *Registry {
	return &Registry{keys: map[OperatorID]ed25519.PublicKey{}}
}

// Add records an operator's public key. Adding the same operator again replaces
// the key, which is how a key rotation propagates.
func (r *Registry) Add(op OperatorID, pub ed25519.PublicKey) {
	r.keys[op] = pub
}

// Key returns an operator's public key and whether the registry knows the
// operator.
func (r *Registry) Key(op OperatorID) (ed25519.PublicKey, bool) {
	pub, ok := r.keys[op]
	return pub, ok
}

// VerifyArtifact checks an artifact against the key the registry holds for its
// operator. It is the consumer's gate: an unknown operator is rejected
// (ErrUnknownOperator) before any verification, and a bad signature is rejected
// (ErrBadSignature). A nil return means the artifact is from a known operator and
// its bytes and provenance are intact.
func (r *Registry) VerifyArtifact(a Artifact) error {
	pub, ok := r.keys[a.Operator]
	if !ok {
		return ErrUnknownOperator
	}
	return Verify(a, pub)
}
