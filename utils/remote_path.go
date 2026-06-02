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
