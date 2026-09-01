package cmd

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// helperProcessTimeout bounds a child that blocks—on the network, or on a
// prompt reading a stdin that never reaches EOF. Without it the whole suite
// waits out go test's own timeout and the child outlives the test binary.
const helperProcessTimeout = 60 * time.Second

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

// runHelperProcess re-executes this test binary as a child so an os.Exit the
// command makes is observable, which no in-process test can see.
func runHelperProcess(t *testing.T, helperTest, marker string, args []string, env ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), helperProcessTimeout)
	defer cancel()

	helperArgs := append([]string{"-test.run=^" + helperTest + "$", "--", marker}, args...)
	helper := osexec.CommandContext(ctx, os.Args[0], helperArgs...)
	helper.Env = append(os.Environ(), env...)

	var stdoutBuf, stderrBuf bytes.Buffer
	helper.Stdout = &stdoutBuf
	helper.Stderr = &stderrBuf

	if err := helper.Run(); err != nil {
		require.NoError(t, ctx.Err(), "%s did not finish within %s", helperTest, helperProcessTimeout)
		var exitErr *osexec.ExitError
		require.ErrorAs(t, err, &exitErr)
		exitCode = exitErr.ExitCode()
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func helperArgsAfter(args []string, marker string) ([]string, bool) { // Skips past the -test flags a re-executed test binary still carries in its own argv.
	if i := slices.Index(args, marker); i >= 0 {
		return args[i+1:], true
	}
	return nil, false
}
