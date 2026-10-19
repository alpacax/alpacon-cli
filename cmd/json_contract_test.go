package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contractLeaf struct {
	path []string
	name string
}

// Hidden commands are skipped (help topics, stand-ins with their own tests); help and completion
// only exist once Cobra executes.
func contractLeaves() []contractLeaf {
	var leaves []contractLeaf
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
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

// assertEnvelopeContract checks the promise every failure makes under --output json.
func assertEnvelopeContract(t *testing.T, stdout, stderr string, code int, wantCommand string) map[string]any {
	t.Helper()
	assert.NotEqual(t, 0, code)

	if strings.TrimSpace(stdout) != "" {
		dec := json.NewDecoder(strings.NewReader(stdout))
		var first any
		require.NoError(t, dec.Decode(&first), "stdout: %q", stdout)
		assert.False(t, dec.More(), "stdout holds more than one JSON value: %q", stdout)
	}

	env := lastEnvelopeOfStderr(t, stderr)
	assert.Equal(t, false, env["ok"])
	assert.NotEmpty(t, env["error_code"])
	assert.InDelta(t, code, env["exit_code"], 0)
	assert.Equal(t, wantCommand, env["command"])
	return env
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

			stdout, stderr, code := runExecuteHelper(t, args...)

			want := leaf.name
			if leaf.path[0] == "kube" {
				want = "kube" // the child has no config, so the surface gate answers for the whole group
			}
			assertEnvelopeContract(t, stdout, stderr, code, want)
		})
	}
}

func TestJSONContractFailuresThatAreNotBadFlags(t *testing.T) {
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

			env := assertEnvelopeContract(t, stdout, stderr, code, tt.wantCommand)
			assert.Equal(t, tt.wantCode, code)
			assert.Equal(t, tt.wantError, env["error_code"])
		})
	}
}
