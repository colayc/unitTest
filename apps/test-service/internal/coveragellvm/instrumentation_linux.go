//go:build linux

package coveragellvm

import (
	"unit-test-ide.local/test-service/internal/coveragerun"
)

func platformInstrumentationCompilers(toolset *Toolset) (coveragerun.TrustedPath, coveragerun.TrustedPath, error) {
	c, cxx := toolset.CCompiler(), toolset.CXXCompiler()
	if !toolset.fourTools || c.Verify() != nil || cxx.Verify() != nil || c.Path() == cxx.Path() {
		return nil, nil, ErrInvalidToolset
	}
	return c, cxx, nil
}
