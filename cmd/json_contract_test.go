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

func TestHelpGoesToStderrUnderJSONOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "help flag on a group", args: []string{"workspace", "--help"}},
		{name: "help flag on an alias", args: []string{"ws", "ls", "-h"}},
		{name: "help command", args: []string{"help", "server"}},
		{name: "help flag on a command with its own parser", args: []string{"exec", "--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, exitCode := runJSONHelper(t, tt.args...)

			assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Usage:")
		})
	}
}

func TestHelpForAnUnknownTopicIsAUsageErrorUnderJSONOutput(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := runJSONHelper(t, "help", "no-such-command")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Empty(t, stdout)
	assert.JSONEq(t, `{"ok":false,"exit_code":2,"error_code":"usage_error","message":"unknown help topic \"no-such-command\"","command":"help","context":{"operation":"help"}}`, stderr)
}

func TestAGroupWithoutASubcommandPrintsHelpToStderrBeforeTheEnvelopeUnderJSONOutput(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := runJSONHelper(t, "server")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Available Commands:")
	assert.Equal(t, "server", lastEnvelope(t, stderr).Command)
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
