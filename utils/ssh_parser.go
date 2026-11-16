package utils

import (
	"runtime"
	"strings"
)

// SSHTarget represents a parsed SSH-like target with user, host, and path components.
type SSHTarget struct {
	User string // Username (empty if not specified)
	Host string
	Path string // Path (empty if not specified)
}

// ParseSSHTarget parses SSH-like target strings and returns the components
// Supports formats:
// - host
// - user@host
// - host:path
// - user@host:path
// An '@' is only treated as the user separator when it precedes the first ':',
// so a remote path may itself contain '@' (e.g. host:/tmp/alice@example.com).
func ParseSSHTarget(target string) SSHTarget {
	result := SSHTarget{}

	result.User, target = splitUserPrefix(target)

	if strings.Contains(target, ":") {
		parts := strings.SplitN(target, ":", 2)
		result.Host = parts[0]
		result.Path = parts[1]
	} else {
		result.Host = target
	}

	return result
}

// IsRemoteTarget reports whether target holds a colon and is not spelled as a local path:
// a ./, ../, .\, ..\, \\ or / prefix, C:\x, or C:/x on Windows.
func IsRemoteTarget(target string) bool {
	return isRemoteTargetOn(runtime.GOOS, target)
}

func isRemoteTargetOn(goos, target string) bool {
	if isExplicitLocalPath(goos, target) {
		return false
	}
	_, target = splitUserPrefix(target)
	return strings.Contains(target, ":")
}

// isExplicitLocalPath: C:\x is a drive path on every OS; C:/x only on Windows, since
// elsewhere it is also server "C" with an absolute path.
func isExplicitLocalPath(goos, target string) bool {
	driveLetter := len(target) >= 3 && isASCIILetter(target[0]) && target[1] == ':'
	if driveLetter && (target[2] == '\\' || (target[2] == '/' && goos == "windows")) {
		return true
	}
	for _, prefix := range []string{"/", "./", "../", `.\`, `..\`, `\\`} {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func splitUserPrefix(target string) (string, string) {
	atIndex := strings.Index(target, "@")
	colonIndex := strings.Index(target, ":")
	if atIndex != -1 && (colonIndex == -1 || atIndex < colonIndex) {
		parts := strings.SplitN(target, "@", 2)
		return parts[0], parts[1]
	}
	return "", target
}

// IsLocalTarget checks if a target string represents a local location.
func IsLocalTarget(target string) bool {
	return !IsRemoteTarget(target)
}
