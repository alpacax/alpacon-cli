//go:build !windows

package runtime

import "syscall"

const descriptorShortage = syscall.EMFILE

var retryableAcceptErrnoCases = map[string]syscall.Errno{
	"EMFILE":       syscall.EMFILE,
	"ENFILE":       syscall.ENFILE,
	"ENOBUFS":      syscall.ENOBUFS,
	"ENOMEM":       syscall.ENOMEM,
	"EPROTO":       syscall.EPROTO,
	"ECONNABORTED": syscall.ECONNABORTED,
	"EINTR":        syscall.EINTR,
}
