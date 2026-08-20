//go:build !windows

package coveragellvm

import (
	"errors"
	"os"
)

func pinInstrumentationRoot(path string) (*instrumentationRootPin, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0o077 != 0 {
		return nil, ErrInvalidToolset
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	pin := &instrumentationRootPin{path: path, file: file, info: before}
	if err := verifyInstrumentationRoot(pin); err != nil {
		_ = file.Close()
		return nil, err
	}
	return pin, nil
}

func verifyInstrumentationRoot(pin *instrumentationRootPin) error {
	if pin == nil || pin.file == nil || pin.info == nil {
		return errors.New("instrumentation root pin is closed")
	}
	before, err := os.Lstat(pin.path)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0o077 != 0 || !os.SameFile(pin.info, before) {
		return errors.New("instrumentation root path changed")
	}
	current, err := pin.file.Stat()
	if err != nil || !current.IsDir() || !os.SameFile(pin.info, current) {
		return errors.New("instrumentation root handle changed")
	}
	after, err := os.Lstat(pin.path)
	if err != nil || !os.SameFile(pin.info, after) {
		return errors.New("instrumentation root changed while validating")
	}
	return nil
}
