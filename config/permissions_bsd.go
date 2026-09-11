//go:build freebsd || netbsd || openbsd || dragonfly

package config

import "syscall"

// The BSDs do not agree with Linux on what O_NOFOLLOW reports for a symbolic
// link: FreeBSD and DragonFly return EMLINK, NetBSD EFTYPE. Reading only ELOOP
// there would report a refused link as an ordinary filesystem failure.
var symlinkErrnos = []error{syscall.ELOOP, syscall.EMLINK, syscall.EFTYPE}
