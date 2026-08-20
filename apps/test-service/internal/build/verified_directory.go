package build

import (
	"errors"
	"os"
	"sync"
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

func (directory *verifiedDirectory) Verify() error {
	if directory == nil {
		return errors.New("verified directory is nil")
	}
	return verifyDirectory(directory)
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
