//go:build !windows

package coverageexec

import "os"

func createOwnerOnlyExecutionDirectory(path string) error {
	return os.Mkdir(path, 0o700)
}
