// Package serviceanchor contains the opaque capability used to bind coverage
// artifacts to one service-owned data directory.  Runtime is the only
// production package that should create an Anchor; build and coveragebundle
// only consume copies of the opaque value.
package serviceanchor

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Anchor is an opaque service-owned provenance token.  The issuer token is
// private, so copying an Anchor preserves identity while constructing one
// without this package's constructor is impossible.
type Anchor struct {
	root  string
	token *issuer
}

type issuer struct {
	nonce uint64
}

var issuerSequence atomic.Uint64

// newAnchor creates the runtime-owned anchor for an already prepared service
// data root. It is deliberately package-private: no sibling package can mint
// an issuer from an arbitrary path. runtime obtains the capability through its
// own leaf-only bridge and publishes only the opaque Anchor value in Layout.
func newAnchor(root string) (Anchor, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return Anchor{}, errors.New("invalid service anchor root")
	}
	nonce := issuerSequence.Add(1)
	if nonce == 0 {
		return Anchor{}, errors.New("service anchor issuer sequence exhausted")
	}
	return Anchor{root: root, token: &issuer{nonce: nonce}}, nil
}

func (anchor Anchor) Verify(path string) error {
	if anchor.token == nil || anchor.root == "" || !pathWithin(anchor.root, path) {
		return errors.New("invalid service-owned provenance")
	}
	return nil
}

func (anchor Anchor) Root() string { return anchor.root }

// SameIssuer checks that two copied anchors came from the same runtime-owned
// issuer, rather than merely having equal root strings.
func (anchor Anchor) SameIssuer(other Anchor) bool {
	return anchor.token != nil && other.token != nil && anchor.token.nonce != 0 && anchor.token == other.token
}

func pathWithin(root, child string) bool {
	root, child = filepath.Clean(root), filepath.Clean(child)
	if filepath.VolumeName(root) != "" && !equalFold(filepath.VolumeName(root), filepath.VolumeName(child)) {
		return false
	}
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func equalFold(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if lower(left[i]) != lower(right[i]) {
			return false
		}
	}
	return true
}

func lower(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}
