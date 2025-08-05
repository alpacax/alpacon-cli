package utils

import (
	"strings"
)

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
func ParseSSHTarget(target string) SSHTarget {
	result := SSHTarget{}
	
	// First, check if there's a user@ prefix
	if strings.Contains(target, "@") {
		parts := strings.SplitN(target, "@", 2)
		result.User = parts[0]
		target = parts[1] // Continue processing with the remainder
	}
	
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
	// First remove any user@ prefix to check the host:path part
	if strings.Contains(target, "@") {
		parts := strings.SplitN(target, "@", 2)
		target = parts[1]
	}
	return strings.Contains(target, ":")
}

// IsLocalTarget checks if a target string represents a local location
func IsLocalTarget(target string) bool {
	return !IsRemoteTarget(target)
}

// FormatSSHTarget formats an SSHTarget back into a string representation
func FormatSSHTarget(target SSHTarget) string {
	result := ""
	
	if target.User != "" {
		result = target.User + "@"
	}
	
	result += target.Host
	
	if target.Path != "" {
		result += ":" + target.Path
	}
	
	return result
}