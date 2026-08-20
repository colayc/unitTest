//go:build !windows

package coverageexec

import "os"

func openRetainedDirectory(path string) (*os.File, error) {
	return os.Open(path)
}
