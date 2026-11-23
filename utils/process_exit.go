package utils

import "runtime"

// ProcessExitCode returns the status this process can exit with for a remote failure's code: never 0,
// and on Unix only the low 8 bits a parent process can read.
func ProcessExitCode(code int) int { return processExitCodeOn(runtime.GOOS, code) }

func processExitCodeOn(goos string, code int) int {
	if goos != "windows" {
		code &= 0xff
	}
	if code == 0 {
		return ExitCodeGeneralError
	}
	return code
}
