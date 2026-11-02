package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contractEnvelope struct {
	OK        bool   `json:"ok"`
	ExitCode  int    `json:"exit_code"`
	ErrorCode string `json:"error_code"`
	Command   string `json:"command"`
	Context   struct {
		Operation string `json:"operation"`
	} `json:"context"`
}

// runJSONHelper runs args under --output json through the real Execute() in a child process with a saved login.
func runJSONHelper(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, "TestExitCodeHelperProcess", exitHelperMarker, append([]string{"--output", "json"}, args...),
		exitHelperEnv+"=1", homeEnvVar()+"="+t.TempDir())
}

func lastEnvelope(t *testing.T, stderr string) contractEnvelope {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	var envelope contractEnvelope
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &envelope), "stderr: %s", stderr)
	return envelope
}

func TestTheEnvelopeNamesTheDeepestCommandTheLineResolved(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "alias resolves to the canonical name", args: []string{"ws", "ls", "--no-such-flag"}, want: "workspace ls"},
		{name: "unknown subcommand stops at its group", args: []string{"server", "no-such-command"}, want: "server"},
		{name: "unknown top level command names nothing", args: []string{"no-such-command"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, stderr, exitCode := runJSONHelper(t, tt.args...)

			assert.Equal(t, utils.ExitCodeUsageError, exitCode)
			envelope := lastEnvelope(t, stderr)
			assert.Equal(t, tt.want, envelope.Command)
			assert.Equal(t, tt.want, envelope.Context.Operation)
		})
	}
}
