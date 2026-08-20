// Package coveragereport renders the immutable, offline representation of a
// canonical Coverage JSON v1 document and a completed test run.
package coveragereport

import (
	"errors"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const (
	maxCoverageJSONBytes = 64 << 20
	maxJUnitXMLBytes     = 64 << 20
	maxCoverageHTMLBytes = 128 << 20
	maxSourceTextBytes   = 1 << 20
	maxDiagnosticBytes   = 4096
)

var ErrInvalidSet = errors.New("invalid coverage report set")

type Input struct {
	CoverageJSON []byte
	Document     coveragemodelv1.CoverageDocumentV1
	TestRun      testdomain.TestRun
	Sources      []coveragenormalize.SourceBinding
}

type Set struct {
	CoverageJSON []byte
	JUnitXML     []byte
	CoverageHTML []byte
	Summary      coveragedomain.Summary
	Sources      []coveragedomain.SourceSnapshot
}
