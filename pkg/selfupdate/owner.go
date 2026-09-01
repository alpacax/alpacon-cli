package selfupdate

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const packageQueryTimeout = 5 * time.Second // rpm takes a read lock on its database, so a package transaction running elsewhere would otherwise stall the update for as long as it lasts.

type CommandRunner func(name string, args ...string) ([]byte, error)

// ExecRunner reads stdout alone: dpkg-query warns on stderr and still exits
// zero, and CombinedOutput would hand the parser "dpkg-query: warning" as the
// package that owns the file.
func ExecRunner(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), packageQueryTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func PackageOwner(run CommandRunner, realPath string) string {
	if run == nil {
		return ""
	}

	if out, err := run("dpkg", "-S", realPath); err == nil {
		if name, _, found := strings.Cut(string(out), ":"); found { // dpkg -S answers "<package>: <path>".
			if name = strings.TrimSpace(name); name != "" {
				return "deb:" + name
			}
		}
	}

	if out, err := run("rpm", "-qf", realPath); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return "rpm:" + name
		}
	}
	return ""
}
