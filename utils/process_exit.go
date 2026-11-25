package utils

import "runtime"

// ProcessExitCode returns the status this process can exit with for a remote failure's code: never 0,
// and only the bits a parent process can read—the low 8 on Unix, the low 32 on Windows.
func ProcessExitCode(code int) int { return processExitCodeOn(runtime.GOOS, code) }

func processExitCodeOn(goos string, code int) int {
	if goos == "windows" {
		code = int(uint32(code)) // ExitProcess takes a 32-bit status, which a parent reads unsigned
	} else {
		code &= 0xff
	}
	if code == 0 {
		return ExitCodeGeneralError
	}
	return code
}
