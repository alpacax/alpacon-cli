package runtime

import (
	"syscall"

	"golang.org/x/sys/windows"
)

const descriptorShortage = windows.WSAEMFILE

var retryableAcceptErrnoCases = map[string]syscall.Errno{
	"WSAEMFILE":       windows.WSAEMFILE,
	"WSAENOBUFS":      windows.WSAENOBUFS,
	"WSAECONNABORTED": windows.WSAECONNABORTED,
}
