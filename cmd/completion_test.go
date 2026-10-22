package cmd

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The packages install stdout as the completion file, so anything else there
// breaks it. Serial: generating the expected script reads RootCmd.
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

			stdout, stderr, exitCode := runHelperProcess(t, "TestCompletionHelperProcess", "completion-helper",
				[]string{"completion", tc.shell}, "GO_WANT_COMPLETION_HELPER=1", homeEnvVar()+"="+t.TempDir())

			assert.Equal(t, 0, exitCode)
			assert.Empty(t, stderr)
			assert.Equal(t, want.String(), stdout)
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
