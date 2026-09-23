//go:build windows

package testrun

func processExitWasCrash(exitCode int) bool {
	code := uint32(exitCode)
	// MSVC abort() uses _exit(3) when WER is disabled, as in the locked
	// framework fixture; native exceptions still use a high-bit status code.
	return code == 3 || code&0x80000000 != 0
}
