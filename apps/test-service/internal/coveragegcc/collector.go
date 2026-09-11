package coveragegcc

import (
	"path/filepath"

	"unit-test-ide.local/test-service/internal/coveragebundle"
	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
)

// PrepareCollector materializes the only supported GCC collection process.
// The caller retains every directory capability on failure. On success the
// returned collector owns the bundle pin and its private gcovr child.
func PrepareCollector(
	bundle coveragebundle.Pin,
	collectorRoot, sourceRoot, objectRoot coverageplatform.DirectoryVerifier,
	gcov coveragerun.TrustedPath,
) (coverageplatform.CollectorExecution, error) {
	if err := coverageplatform.VerifyDirectory(collectorRoot); err != nil {
		return nil, err
	}
	if err := coverageplatform.VerifyDirectory(sourceRoot); err != nil {
		return nil, err
	}
	if err := coverageplatform.VerifyDirectory(objectRoot); err != nil {
		return nil, err
	}
	input := coveragebundle.DescriptorInput{
		Root:            sourceRoot.Path(),
		ObjectDirectory: objectRoot.Path(),
		GcovExecutable:  gcov.Path(),
		OutputPath:      filepath.Join(collectorRoot.Path(), "gcovr", "coverage.json"),
	}
	return coveragebundle.PrepareRunner(bundle, input, coveragebundle.DescriptorCapabilities{
		CollectorRoot:   collectorRoot,
		Root:            sourceRoot,
		ObjectDirectory: objectRoot,
		GcovExecutable:  gcov,
	})
}
