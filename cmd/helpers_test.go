package cmd

import (
	"runtime"
	"testing"
)

func homeEnvVar() string { // A test that sets only HOME points the Windows run at the real user's home, so the config it wrote under t.TempDir() is never the one being read.
	if runtime.GOOS == "windows" {
		return "USERPROFILE"
	}
	return "HOME"
}

func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(homeEnvVar(), dir)
}

func helperArgsAfter(args []string, marker string) ([]string, bool) { // Skips past the -test flags a re-executed test binary still carries in its own argv.
	for i := 0; i < len(args); i++ {
		if args[i] == marker {
			return args[i+1:], true
		}
	}
	return nil, false
}
