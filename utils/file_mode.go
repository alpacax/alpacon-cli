package utils

import "runtime"

// HasUnixModes reports whether the OS has Unix permission bits. Windows has
// none, and Chmod there toggles the read-only attribute instead.
func HasUnixModes() bool {
	return runtime.GOOS != "windows"
}
