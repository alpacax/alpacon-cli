package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Same cap filepath.walkSymlinks applies, so a zip path gives up where a plain one does.
const maxZipSymlinkHops = 255

func resolveZipPath(root *os.Root, rootNames []string, name string) (string, error) {
	pending := strings.Split(name, string(os.PathSeparator))
	var resolved []string
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return "", fmt.Errorf("zip path escapes destination: %s", name)
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		candidate := filepath.Join(filepath.Join(resolved...), part)
		info, err := root.Lstat(candidate)
		if os.IsNotExist(err) {
			resolved = append(resolved, part)
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if len(pending) > 0 && !info.IsDir() {
				return "", fmt.Errorf("zip path component is not a directory: %s", candidate)
			}
			resolved = append(resolved, part)
			continue
		}
		links++
		if links > maxZipSymlinkHops {
			return "", fmt.Errorf("too many symlinks in zip path: %s", name)
		}
		target, err := root.Readlink(candidate)
		if err != nil {
			return "", err
		}
		target = filepath.FromSlash(target)
		if filepath.IsAbs(target) {
			target, err = zipRelativeTarget(rootNames, target)
			if err != nil {
				return "", err
			}
			resolved = nil
		} else if filepath.VolumeName(target) != "" || strings.HasPrefix(target, string(os.PathSeparator)) {
			return "", fmt.Errorf("zip symlink escapes destination: %s", candidate)
		}
		// Expand links before consuming '..'; cleaning first can select a different file.
		pending = append(strings.Split(target, string(os.PathSeparator)), pending...)
	}
	if len(resolved) == 0 {
		return ".", nil
	}
	return filepath.Join(resolved...), nil
}

func zipRelativeTarget(rootNames []string, target string) (string, error) {
	for _, name := range rootNames {
		prefix := strings.TrimRight(name, string(os.PathSeparator)) + string(os.PathSeparator)
		if zipPathEqual(target, name) {
			return ".", nil
		}
		if len(target) >= len(prefix) && zipPathEqual(target[:len(prefix)], prefix) {
			return target[len(prefix):], nil
		}
	}
	return "", fmt.Errorf("zip symlink escapes destination: %s", target)
}

func zipPathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
