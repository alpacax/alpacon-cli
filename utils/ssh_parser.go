package utils

import "strings"

// SSHTarget represents a parsed SSH-like target with user, host, and path components
type SSHTarget struct {
	User string // Username (empty if not specified)
	Host string // Hostname/server name
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

	// Now check if there's a :path suffix
	if strings.Contains(target, ":") {
		parts := strings.SplitN(target, ":", 2)
		result.Host = parts[0]
		result.Path = parts[1]
	} else {
		result.Host = target
	}

	return result
}

// IsRemoteTarget checks if a target string represents a remote location
// A target is considered remote if it contains a colon (:)
func IsRemoteTarget(target string) bool {
	_, target = splitUserPrefix(target)
	return strings.Contains(target, ":")
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

// IsLocalTarget checks if a target string represents a local location
func IsLocalTarget(target string) bool {
	return !IsRemoteTarget(target)
}
