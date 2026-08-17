package runtime

import (
	"unsafe"

	"unit-test-ide.local/test-service/internal/serviceanchor"
)

// newServiceAnchor is linked to the package-private serviceanchor factory.
// Keeping the bridge here means the runtime owns issuance while the leaf
// package exposes no exported path-minting API to sibling packages.
//
//go:linkname newServiceAnchor unit-test-ide.local/test-service/internal/serviceanchor.newAnchor
func newServiceAnchor(root string) (serviceanchor.Anchor, error)

// Keep unsafe imported in this file as required by go:linkname.
var _ unsafe.Pointer
