package coveragebundle

import (
	"unsafe"

	"unit-test-ide.local/test-service/internal/serviceanchor"
)

// Test-only bridge: production packages cannot mint anchors. This unsafe
// declaration exists solely to build real capability fixtures and is covered
// by runtime's static production-linkname check.
//
//go:linkname testNewServiceAnchor unit-test-ide.local/test-service/internal/serviceanchor.newAnchor
func testNewServiceAnchor(root string) (serviceanchor.Anchor, error)

var _ unsafe.Pointer
