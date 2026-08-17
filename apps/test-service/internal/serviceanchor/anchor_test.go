package serviceanchor

import (
	"path/filepath"
	"testing"
)

func TestAnchorIssuerIsNonZeroAndIdentityBound(t *testing.T) {
	first, err := newAnchor(filepath.Join(t.TempDir(), "one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := newAnchor(filepath.Join(t.TempDir(), "two"))
	if err != nil {
		t.Fatal(err)
	}
	if first.token == nil || first.token.nonce == 0 || second.token == nil || second.token.nonce == 0 {
		t.Fatalf("anchor issuer nonce is zero: first=%#v second=%#v", first.token, second.token)
	}
	if !first.SameIssuer(first) {
		t.Fatal("anchor did not recognize its copied issuer")
	}
	if first.SameIssuer(second) {
		t.Fatal("distinct anchors share an issuer")
	}
}
