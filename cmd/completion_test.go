package cmd

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Packages install stdout as the completion file, and Homebrew runs it with no
// Alpacon config. Serial: the child it runs is a helper process.
func TestCompletionWritesOnlyTheScriptToStdout(t *testing.T) {
	tests := []struct {
		shell string
		gen   func(*cobra.Command, io.Writer) error
	}{
		{shell: "bash", gen: func(root *cobra.Command, w io.Writer) error { return root.GenBashCompletionV2(w, true) }},
		{shell: "zsh", gen: (*cobra.Command).GenZshCompletion},
		{shell: "fish", gen: func(root *cobra.Command, w io.Writer) error { return root.GenFishCompletion(w, true) }},
	}
	for _, tc := range tests {
		t.Run(tc.shell, func(t *testing.T) {
			var want bytes.Buffer
			require.NoError(t, tc.gen(newRootCmd(cmdtest.NoClient, utils.Stdio), &want))

			home := t.TempDir()

			stdout, stderr, exitCode := runHelperProcess(t, "TestCompletionHelperProcess", "completion-helper",
				[]string{"completion", tc.shell}, "GO_WANT_COMPLETION_HELPER=1", homeEnvVar()+"="+home)

			assert.Equal(t, 0, exitCode)
			assert.Empty(t, stderr)
			assert.Equal(t, want.String(), stdout)
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}

func TestCompletionHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_COMPLETION_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "completion-helper")
	if !ok {
		os.Exit(2)
	}
	os.Args = append([]string{"alpacon"}, args...)
	Execute()
	os.Exit(0)
}
