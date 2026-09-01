package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrOwnerUnknown separates "no package owns this" from "the query could not
// answer". Both used to arrive as the empty string, which ClassifyPath reads as
// a manual install and Run reads as permission to overwrite the file.
var ErrOwnerUnknown = errors.New("cannot tell which package owns the binary")

var packageQueryTimeout = 5 * time.Second // rpm takes a read lock on its database, so a package transaction running elsewhere would otherwise stall the update for as long as it lasts.

// packageManagerPaths pins where a manager may live. $PATH would otherwise name
// it, and 'sudo alpacon update'—the command this CLI prints itself—carries the
// operator's own PATH wherever sudoers has no secure_path.
var packageManagerPaths = map[string][]string{
	"dpkg": {"/usr/bin/dpkg", "/bin/dpkg"},
	"rpm":  {"/usr/bin/rpm", "/bin/rpm"},
}

// CommandRunner answers who owns a path. A nil error is an answer; so is an
// ordinary error, which means this manager does not claim the file. Only an
// error wrapping ErrOwnerUnknown says the question went unanswered.
type CommandRunner func(name string, args ...string) ([]byte, error)

// ExecRunner reads stdout alone: dpkg-query warns on stderr and still exits
// zero, and CombinedOutput would hand the parser "dpkg-query: warning" as the
// package that owns the file.
func ExecRunner(name string, args ...string) ([]byte, error) {
	path, installed := resolvePackageManager(name)
	if !installed {
		return nil, fmt.Errorf("%s is not installed at a trusted path", name) // An answer, not uncertainty: this manager does not run on this host.
	}

	ctx, cancel := context.WithTimeout(context.Background(), packageQueryTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err == nil {
		return out, nil
	}

	// The deadline first: a killed process reports an ExitError like any other
	// non-zero exit, and rpm is killed mid-query exactly when another
	// transaction holds its database.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOwnerUnknown, name, ctx.Err())
	}

	// A non-zero exit is an answer, and so is a manager not installed on this host.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) || errors.Is(err, exec.ErrNotFound) {
		return nil, err
	}
	return nil, fmt.Errorf("%w: %s: %w", ErrOwnerUnknown, name, err)
}

func resolvePackageManager(name string) (string, bool) {
	candidates, pinned := packageManagerPaths[name]
	if !pinned {
		return name, true
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func PackageOwner(run CommandRunner, executablePath string) (string, error) {
	if run == nil {
		return "", fmt.Errorf("%w: no package-manager query was supplied", ErrOwnerUnknown)
	}

	out, err := queryOwner(run, "dpkg", "-S", executablePath)
	if err != nil {
		return "", err
	}
	if name, _, found := strings.Cut(out, ":"); found { // dpkg -S answers "<package>: <path>".
		if name = strings.TrimSpace(name); name != "" {
			return "deb:" + name, nil
		}
	}

	out, err = queryOwner(run, "rpm", "-qf", executablePath)
	if err != nil {
		return "", err
	}
	if name := strings.TrimSpace(out); name != "" {
		return "rpm:" + name, nil
	}
	return "", nil
}

// queryOwner returns empty output for a manager that answered without claiming
// the file, and an error only when the query could not answer at all.
func queryOwner(run CommandRunner, tool string, args ...string) (string, error) {
	out, err := run(tool, args...)
	if err == nil {
		return string(out), nil
	}
	if errors.Is(err, ErrOwnerUnknown) {
		return "", err
	}
	return "", nil
}
