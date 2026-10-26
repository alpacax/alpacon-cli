package cmd

import (
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contractLeaf struct {
	path []string
	name string
}

// contractLeaves skips hidden commands, which are stand-ins with tests of their own.
func contractLeaves() []contractLeaf {
	RootCmd.InitDefaultHelpCmd()
	RootCmd.InitDefaultCompletionCmd()
	var leaves []contractLeaf
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden {
				continue
			}
			if sub.Runnable() {
				name := commandName(sub)
				leaves = append(leaves, contractLeaf{path: strings.Fields(name), name: name})
			}
			walk(sub)
		}
	}
	walk(RootCmd)
	return leaves
}

func assertEnvelopeContract(t *testing.T, stdout, stderr string, code int, wantCommand, wantError string) {
	t.Helper()
	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	env := lastEnvelopeOfStderr(t, stderr)
	assert.Equal(t, false, env["ok"])
	assert.Equal(t, wantError, env["error_code"])
	assert.InDelta(t, code, env["exit_code"], 0)
	assert.Equal(t, wantCommand, env["command"])
}

// Serial: the walk reads package-level Cobra commands. The children are separate processes.
func TestJSONContractEveryLeafFailsWithAnEnvelope(t *testing.T) {
	leaves := contractLeaves()
	require.Greater(t, len(leaves), 50)

	for _, leaf := range leaves {
		t.Run(strings.ReplaceAll(leaf.name, " ", "_"), func(t *testing.T) {
			t.Parallel()
			// Flags go straight after the command path, so exec and websh (DisableFlagParsing)
			// see them before any positional argument.
			args := append(append([]string{}, leaf.path...), "--output", "json", "--definitely-not-a-flag")
			wantCommand, wantError := leaf.name, utils.ErrorCodeUsage
			switch leaf.path[0] {
			case "websh":
				args = append(args[:len(args)-1], "my-server", "--read-only=maybe") // websh reads an unknown flag as the server name
			case "kube":
				wantCommand, wantError = "kube", utils.ErrorCodeCLI // no config in the child, so the surface gate answers for the group
			}

			stdout, stderr, code := runExecuteHelper(t, args...)

			assertEnvelopeContract(t, stdout, stderr, code, wantCommand, wantError)
		})
	}
}

func TestJSONContractFailuresThatAreNotBadFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		wantCode    int
		wantCommand string
		wantError   string
	}{
		{"group without subcommand", []string{"server"}, 1, "server", "usage_error"},
		{"missing positional argument", []string{"server", "describe"}, 1, "server describe", "usage_error"},
		{"missing required flag exits 2", []string{"event", "wait"}, 2, "event wait", "usage_error"},
		{"server ls without a login on file", []string{"server", "ls"}, 1, "server ls", "not_logged_in"},
		{"exec without a server", []string{"exec"}, 1, "exec", "usage_error"},
		{"exec without a command", []string{"exec", "my-server"}, 1, "exec", "usage_error"},
		{"exec flag without value", []string{"exec", "-u"}, 1, "exec", "usage_error"},
		{"websh without a server", []string{"websh"}, 1, "websh", "usage_error"},
		{"cp without arguments", []string{"cp"}, 1, "cp", "usage_error"},
		{"cp with two local paths", []string{"cp", "a", "b"}, 1, "cp", "usage_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"--output", "json"}, tt.args...)

			stdout, stderr, code := runExecuteHelper(t, args...)

			assertEnvelopeContract(t, stdout, stderr, code, tt.wantCommand, tt.wantError)
			assert.Equal(t, tt.wantCode, code)
		})
	}
}
