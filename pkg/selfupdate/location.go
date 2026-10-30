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
func ClassifyPath(executablePath, packageOwner string) InstallKind {
	if kind := classifyByPath(executablePath); kind != "" {
		return kind
	}

	switch {
	case strings.HasPrefix(packageOwner, "deb:"):
		return InstallDeb
	case strings.HasPrefix(packageOwner, "rpm:"):
		return InstallRPM
	}
	return InstallManual
}

// classifyByPath answers only where the path settles it on its own, and "" when
// it does not.
func classifyByPath(executablePath string) InstallKind {
	for segment := range strings.SplitSeq(filepath.ToSlash(executablePath), "/") {
		switch segment {
		case "Cellar":
			return InstallHomebrew
		case "mise", ".asdf":
			return InstallVersionManager
		}
	}
	return ""
}

func DetectInstallKind(run CommandRunner, executablePath string) (InstallKind, error) { // Both halves together: asking either alone can give a different verdict.
	if kind := classifyByPath(executablePath); kind != "" { // Skips the package queries when the path settles it.
		return kind, nil
	}

	owner, err := PackageOwner(run, executablePath)
	if err != nil {
		return "", err
	}
	return ClassifyPath(executablePath, owner), nil
}

func ResolveExecutablePath() (string, error) {
	executable, err := osExecutable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(executable)
}
