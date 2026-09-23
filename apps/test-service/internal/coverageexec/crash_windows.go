//go:build windows

package coverageexec

func processExitWasCrash(exitCode int) bool {
	code := uint32(exitCode)
	return code == 3 || code&0x80000000 != 0
}
