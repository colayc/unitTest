package build

import (
	"errors"
	"os"
	"sync"

	"unit-test-ide.local/test-service/internal/coverageplatform"
)

type verifiedDirectory struct {
	path     string
	identity string
	file     *os.File
	info     os.FileInfo
	native   directoryNativeIdentity
	once     sync.Once
	err      error
}

func (directory *verifiedDirectory) Path() string {
	if directory == nil {
		return ""
	}
	return directory.path
}

func (directory *verifiedDirectory) Verify() error {
	if directory == nil {
		return errors.New("verified directory is nil")
	}
	return verifyDirectory(directory)
}

// RetainDirectory creates a distinct native pin for the same direct
// directory. It verifies both the source and clone before publication, so a
// caller never receives an unverified pathname-derived facade.
func (directory *verifiedDirectory) RetainDirectory() (coverageplatform.RetainedDirectory, error) {
	if directory == nil || directory.Verify() != nil {
		return nil, errors.New("verified directory is unavailable")
	}
	retained, err := pinVerifiedDirectory(directory.path)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (coverageplatform.RetainedDirectory, error) {
		_ = retained.Close()
		return nil, cause
	}
	if retained.path != directory.path || directory.Verify() != nil || retained.Verify() != nil {
		return fail(errors.New("verified directory changed while retaining"))
	}
	return retained, nil
}

func (directory *verifiedDirectory) Close() error {
	if directory == nil {
		return nil
	}
	directory.once.Do(func() {
		if directory.file != nil {
			directory.err = directory.file.Close()
			directory.file = nil
		}
	})
	return directory.err
}
