package cmd

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Packages install stdout as the completion file, and Homebrew runs it with no
// Alpacon config. Serial: the expected script comes from RootCmd.
func TestCompletionWritesOnlyTheScriptToStdout(t *testing.T) {
	tests := []struct {
		shell string
		gen   func(io.Writer) error
	}{
		{shell: "bash", gen: func(w io.Writer) error { return RootCmd.GenBashCompletionV2(w, true) }},
		{shell: "zsh", gen: RootCmd.GenZshCompletion},
		{shell: "fish", gen: func(w io.Writer) error { return RootCmd.GenFishCompletion(w, true) }},
	}
	for _, tc := range tests {
		t.Run(tc.shell, func(t *testing.T) {
			var want bytes.Buffer
			require.NoError(t, tc.gen(&want))

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
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}
