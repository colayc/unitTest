//go:build !windows

package coverageexec

func processExitWasCrash(exitCode int) bool {
	return exitCode < 0
}
