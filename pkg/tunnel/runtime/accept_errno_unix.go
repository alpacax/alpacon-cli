//go:build !windows

package runtime

import "syscall"

// retryableAcceptErrnos fail one Accept, not the listener: a descriptor or buffer shortage ends once a connection closes.
var retryableAcceptErrnos = []syscall.Errno{syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.EPROTO, syscall.ECONNABORTED, syscall.EINTR}
