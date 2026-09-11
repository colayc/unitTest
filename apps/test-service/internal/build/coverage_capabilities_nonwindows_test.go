//go:build !windows

package build

import (
	"errors"

	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
)

// These small capability fakes are shared by platform-neutral planner tests.
// The broader capability ownership tests remain Windows-only because their
// fixture helper lives with the Windows coverage isolation tests.
type capabilityPath struct {
	path string
	err  error
}

func (path capabilityPath) Path() string  { return path.path }
func (path capabilityPath) Verify() error { return path.err }

type capabilityClaim struct{ toolset *capabilityToolset }

func (*capabilityClaim) Commit() {}
func (claim *capabilityClaim) Rollback() {
	if claim != nil && claim.toolset != nil {
		claim.toolset.claimed = false
	}
}

type capabilityToolset struct {
	version, identity string
	tools             []coveragerun.TrustedPath
	claimed, valid    bool
	closed            int
}

func (toolset *capabilityToolset) Version() string  { return toolset.version }
func (toolset *capabilityToolset) Identity() string { return toolset.identity }
func (toolset *capabilityToolset) CCompiler() coveragerun.TrustedPath {
	if len(toolset.tools) == 0 {
		return capabilityPath{}
	}
	return capabilityPath{path: toolset.tools[0].Path()}
}
func (toolset *capabilityToolset) CXXCompiler() coveragerun.TrustedPath {
	if len(toolset.tools) == 0 {
		return capabilityPath{}
	}
	return capabilityPath{path: toolset.tools[0].Path()}
}
func (toolset *capabilityToolset) Tools() []coveragerun.TrustedPath {
	return append([]coveragerun.TrustedPath(nil), toolset.tools...)
}
func (toolset *capabilityToolset) Verify() error {
	if toolset == nil || toolset.closed != 0 {
		return errors.New("closed")
	}
	return nil
}
func (toolset *capabilityToolset) ClaimOwnership() (coverageplatform.OwnershipClaim, error) {
	if toolset == nil || toolset.claimed || toolset.closed != 0 {
		return nil, errors.New("already claimed")
	}
	toolset.claimed = true
	return &capabilityClaim{toolset: toolset}, nil
}
func (toolset *capabilityToolset) Close() error {
	toolset.closed++
	return nil
}

var _ coverageplatform.Toolset = (*capabilityToolset)(nil)
var _ coveragerun.TrustedPath = capabilityPath{}
