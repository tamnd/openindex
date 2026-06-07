package open

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestAddressIsStable(t *testing.T) {
	a := Address([]byte("the same bytes"))
	b := Address([]byte("the same bytes"))
	if a != b {
		t.Fatal("the same bytes must content-address to the same hash")
	}
	if a == Address([]byte("different bytes")) {
		t.Fatal("different bytes must not collide")
	}
}

func TestSigningBytesCoverIdentity(t *testing.T) {
	base := Artifact{Kind: KindCIFF, Snapshot: "snap-1", Content: Address([]byte("x")), Operator: "op-a"}
	// Changing any identity field must change the signing bytes, or a signature
	// would carry over to a different artifact.
	variants := []Artifact{
		{Kind: KindWARC, Snapshot: "snap-1", Content: base.Content, Operator: "op-a"},
		{Kind: KindCIFF, Snapshot: "snap-2", Content: base.Content, Operator: "op-a"},
		{Kind: KindCIFF, Snapshot: "snap-1", Content: Address([]byte("y")), Operator: "op-a"},
		{Kind: KindCIFF, Snapshot: "snap-1", Content: base.Content, Operator: "op-b"},
	}
	want := string(base.SigningBytes())
	for i, v := range variants {
		if string(v.SigningBytes()) == want {
			t.Fatalf("variant %d shares signing bytes with the base artifact", i)
		}
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	a := Artifact{Kind: KindCIFF, Snapshot: "snap-1", Content: Address([]byte("index bytes")), Operator: "op-a"}
	a.Sig = Sign(a, priv)
	if err := Verify(a, pub); err != nil {
		t.Fatalf("a freshly signed artifact should verify: %v", err)
	}
	// Tamper with the content address: verification must fail.
	a.Content = Address([]byte("swapped bytes"))
	if err := Verify(a, pub); err == nil {
		t.Fatal("a tampered artifact must not verify")
	}
}

func TestRegistryGatesOnOperator(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	reg := NewRegistry()
	a := Artifact{Kind: KindWARC, Snapshot: "snap-1", Content: Address([]byte("corpus")), Operator: "op-a"}
	a.Sig = Sign(a, priv)

	if err := reg.VerifyArtifact(a); !errors.Is(err, ErrUnknownOperator) {
		t.Fatalf("an unregistered operator should be rejected, got %v", err)
	}
	reg.Add("op-a", pub)
	if err := reg.VerifyArtifact(a); err != nil {
		t.Fatalf("a registered operator's intact artifact should verify: %v", err)
	}

	// A different operator's key must not validate op-a's signature.
	other, _, _ := ed25519.GenerateKey(nil)
	reg.Add("op-a", other)
	if err := reg.VerifyArtifact(a); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("the wrong key should fail with a bad signature, got %v", err)
	}
}

func TestArtifactKindString(t *testing.T) {
	for k, want := range map[ArtifactKind]string{
		KindWARC: "warc", KindCIFF: "ciff", KindWebGraph: "webgraph", KindEmbeddings: "embeddings",
	} {
		if got := k.String(); got != want {
			t.Fatalf("kind %d rendered %q, want %q", k, got, want)
		}
	}
}
