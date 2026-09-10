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

// zipRoot is one spelling of the destination, paired with the prefix an absolute
// symlink target must carry to be inside it. Both are fixed for a whole Unzip call.
type zipRoot struct {
	name   string
	prefix string
}

func newZipRoots(names ...string) []zipRoot {
	roots := make([]zipRoot, 0, len(names))
	for _, name := range names {
		roots = append(roots, zipRoot{
			name:   name,
			prefix: strings.TrimRight(name, string(os.PathSeparator)) + string(os.PathSeparator),
		})
	}
	return roots
}

// resolveZipPath maps an archive entry name to a path relative to root, with every
// symlink expanded, and rejects anything that leaves the destination. Confinement
// does not rest on it: os.Root is the boundary, so a bug here can misplace a file
// inside the destination but never outside it.
//
// os.Root refuses an absolute symlink even when its target is inside the root, so
// permitting one is what these hops cost; failing closed instead would be about
// five lines. We resolve and follow the link because the destination is a folder
// the user chose and may already have populated, and because an archive naming
// "link" asks for what "link" points at.
func resolveZipPath(root *os.Root, roots []zipRoot, name string) (string, error) {
	pending := strings.Split(name, string(os.PathSeparator))
	var resolved []string
	missing := false
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
			// Everything after the first absent component is absent too, so the
			// kernel would stop here rather than let '..' select a sibling.
			if missing {
				return "", fmt.Errorf("zip path traverses a missing directory: %s", name)
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		// resolved holds clean single components and part carries no separator,
		// so joining the strings is what filepath.Join would return anyway.
		candidate := strings.Join(append(resolved, part), string(os.PathSeparator))
		info, err := root.Lstat(candidate)
		if os.IsNotExist(err) {
			missing = true
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
			target, err = zipRelativeTarget(roots, target)
			if err != nil {
				return "", err
			}
			resolved = nil
			missing = false
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

func zipRelativeTarget(roots []zipRoot, target string) (string, error) {
	for _, r := range roots {
		if zipPathEqual(target, r.name) {
			return ".", nil
		}
		if len(target) >= len(r.prefix) && zipPathEqual(target[:len(r.prefix)], r.prefix) {
			return target[len(r.prefix):], nil
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
