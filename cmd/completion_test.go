package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The packages install stdout as the completion file, so anything else there
// breaks it: zsh skips an _alpacon whose first line is not #compdef.
func TestCompletionWritesOnlyTheScriptToStdout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		shell  string
		header string
	}{
		{shell: "bash", header: "# bash completion V2 for alpacon"},
		{shell: "zsh", header: "#compdef alpacon\n"},
		{shell: "fish", header: "# fish completion for alpacon"},
	}
	for _, tc := range tests {
		t.Run(tc.shell, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()

			stdout, stderr, exitCode := runHelperProcess(t, "TestCompletionHelperProcess", "completion-helper",
				[]string{"completion", tc.shell}, "GO_WANT_COMPLETION_HELPER=1", homeEnvVar()+"="+home)

			assert.Equal(t, 0, exitCode)
			assert.Empty(t, stderr)
			firstLine, _, _ := strings.Cut(stdout, "\n")
			assert.True(t, strings.HasPrefix(stdout, tc.header), "stdout starts with %q", firstLine)
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
