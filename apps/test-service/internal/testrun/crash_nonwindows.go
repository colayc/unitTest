//go:build !windows

package testrun

func processExitWasCrash(exitCode int) bool {
	return exitCode < 0
}
