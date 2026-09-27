//go:build !linux

package coveragellvm

import "unit-test-ide.local/test-service/internal/coveragerun"

func platformInstrumentationCompilers(toolset *Toolset) (coveragerun.TrustedPath, coveragerun.TrustedPath, error) {
	c := toolset.CCompiler()
	if c.Verify() != nil {
		return nil, nil, ErrInvalidToolset
	}
	return c, toolset.CXXCompiler(), nil
}
