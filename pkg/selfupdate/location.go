package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	InstallManual         InstallKind = "manual"
	InstallHomebrew       InstallKind = "homebrew"
	InstallDeb            InstallKind = "deb"
	InstallRPM            InstallKind = "rpm"
	InstallVersionManager InstallKind = "version-manager"
)

var osExecutable = os.Executable

type InstallKind string

// ClassifyPath reads the path before the ownership answer: a Homebrew Cellar
// path is unambiguous, while /usr/local/bin is shared by the deb, the rpm, and
// a hand-placed binary, which only the ownership query separates.
func ClassifyPath(realPath, packageOwner string) InstallKind {
	for _, segment := range strings.Split(filepath.ToSlash(realPath), "/") {
		switch segment {
		case "Cellar":
			return InstallHomebrew
		case "mise", ".asdf":
			return InstallVersionManager
		}
	}

	switch {
	case strings.HasPrefix(packageOwner, "deb:"):
		return InstallDeb
	case strings.HasPrefix(packageOwner, "rpm:"):
		return InstallRPM
	}
	return InstallManual
}

func DetectInstallKind(run CommandRunner, realPath string) InstallKind { // Keeps the two halves together: a caller that asks only one of them gets a different verdict from the other's.
	return ClassifyPath(realPath, PackageOwner(run, realPath))
}

func ResolveExecutablePath() (string, error) {
	executable, err := osExecutable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(executable)
}
