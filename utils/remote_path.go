package utils

import (
	"fmt"
	pathpkg "path"
	"path/filepath"
	"strings"
)

// RemoteFileName returns the final path component for a remote file path.
func RemoteFileName(remotePath string) (string, error) {
	name := pathpkg.Base(remotePath)
	if remotePath == "" ||
		strings.HasSuffix(remotePath, "/") ||
		name == "." ||
		name == ".." ||
		strings.Contains(name, `\`) ||
		filepath.IsAbs(name) ||
		filepath.VolumeName(name) != "" ||
		filepath.Base(name) != name {
		return "", fmt.Errorf("remote path must include a file name: %s", remotePath)
	}
	return name, nil
}

// RemotePathBase returns a remote path's last element by the server's rules: a Windows server
// also splits on a backslash, which a Linux or macOS file name may hold.
func RemotePathBase(remotePath string, windows bool) string {
	if !windows {
		return filepath.Base(remotePath) // the client's separators, as before: the name is saved locally and sent as Name
	}
	_, rest := splitWindowsVolume(remotePath)
	return pathpkg.Base(rest)
}

// RemotePathDir returns the directory holding a remote path's last element by the server's rules.
func RemotePathDir(remotePath string, windows bool) string {
	if !windows {
		return pathpkg.Dir(remotePath)
	}
	vol, rest := splitWindowsVolume(remotePath)
	if rest == "" && len(vol) > 2 { // a UNC share is always rooted
		rest = "/"
	}
	return vol + strings.ReplaceAll(pathpkg.Dir(rest), "/", `\`)
}

// splitWindowsVolume splits a Windows path into its drive (C:) or UNC share (\\host\share)
// and the rest, with the rest's separators turned into slashes.
func splitWindowsVolume(remotePath string) (vol, rest string) {
	p := strings.ReplaceAll(remotePath, `\`, "/")
	switch {
	case len(p) >= 2 && isASCIILetter(p[0]) && p[1] == ':':
		return remotePath[:2], p[2:]
	case strings.HasPrefix(p, "//"):
		end := 2
		for range 2 { // host, then share
			next := strings.IndexByte(p[end:], '/')
			if next < 0 {
				return remotePath, ""
			}
			end += next + 1
		}
		return remotePath[:end-1], p[end-1:]
	}
	return "", p
}
