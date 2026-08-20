//go:build windows

package coverageexec

func processExitWasCrash(exitCode int) bool {
	return exitCode != 0 && uint32(exitCode)&0x80000000 != 0
}
