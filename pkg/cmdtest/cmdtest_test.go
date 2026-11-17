package cmdtest

import (
	"errors"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// Serial: it replaces os.Args, which Cobra reads in place of a nil argument list.
func TestRunWithoutArgumentsIgnoresTheTestBinarysArguments(t *testing.T) {
	saved := os.Args
	os.Args = []string{"cmdtest.test", "-test.run", "TestX"} // a space-separated flag leaves TestX positional
	t.Cleanup(func() { os.Args = saved })
	ran := false
	cmd := &cobra.Command{Use: "probe", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		ran = true
		return nil
	}}

	failure, code := Run(cmd)

	assert.Equal(t, 0, code, failure)
	assert.True(t, ran, "the command must run")
}

func TestRunExitsAsExecuteDoes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		runErr   error
		wantCode int
	}{
		{name: "a hook's plain error exits 1", runErr: errors.New("failed to read input"), wantCode: utils.ExitCodeGeneralError},
		{name: "a hook's usage error exits 2", runErr: utils.MarkUsageError(errors.New("bad flag")), wantCode: utils.ExitCodeUsageError},
		{name: "an unknown flag exits 2", args: []string{"--nope"}, wantCode: utils.ExitCodeUsageError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return tt.runErr }}

			failure, code := Run(cmd, tt.args...)

			assert.Equal(t, tt.wantCode, code, failure)
		})
	}
}

func TestRunLeavesTheCommandsHooksAsTheyWere(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return errors.New("boom") }}
	_, _ = Run(cmd)

	err := cmd.RunE(cmd, nil)

	assert.False(t, utils.IsHookError(err), "Run must restore the original RunE")
}

func TestRunCapturedCapturesCobrasOwnOutput(t *testing.T) {
	t.Parallel()
	build := func(*utils.IOStreams) *cobra.Command {
		return &cobra.Command{Use: "probe", Short: "Probe the streams", RunE: func(*cobra.Command, []string) error { return nil }}
	}

	stdout, _, code := RunCaptured(build, "--help")

	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Probe the streams")
}

func TestRunPrintsAParseErrorInTheOutputItsArgumentsName(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().String("output", "", "")

	failure, code := Run(cmd, "--bogus", "--output", "json")

	assert.Equal(t, utils.ExitCodeUsageError, code)
	assert.Contains(t, failure, `"error_code":"usage_error"`)
}
