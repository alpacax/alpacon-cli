package runtime

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// retryableAcceptErrnos fail one Accept, not the listener: Winsock reports a socket shortage as WSAEMFILE or WSAENOBUFS.
var retryableAcceptErrnos = []syscall.Errno{windows.WSAEMFILE, windows.WSAENOBUFS, windows.WSAECONNABORTED}
