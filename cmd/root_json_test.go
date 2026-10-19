package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnFlagArgsStopsAtTheServerAndTheSeparator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"flag then server then remote flag", []string{"--output", "json", "srv", "ls", "--output", "json"}, []string{"--output", "json"}},
		{"remote command carries its own output flag", []string{"srv", "--output", "json"}, []string{}},
		{"value flag hides a server-looking value", []string{"-u", "alice", "--output=json", "srv", "x"}, []string{"-u", "alice", "--output=json"}},
		{"separator ends own flags", []string{"--output", "json", "--", "srv", "--output", "table"}, []string{"--output", "json"}},
		{"read-only boolean value", []string{"--read-only", "true", "--output=json", "srv"}, []string{"--read-only", "true", "--output=json"}},
		{"flags only", []string{"--output=json"}, []string{"--output=json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, append([]string{}, ownFlagArgs(tt.in)...))
		})
	}
}

func TestWebshOwnArgsFollowsTheParserPastTheServer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"flags after the server", []string{"my-server", "--output", "json", "--read-only=maybe"}, []string{"my-server", "--output", "json", "--read-only=maybe"}},
		{"stops at the first command token", []string{"my-server", "--share", "ls", "--output", "json"}, []string{"my-server", "--share"}},
		{"value flag hides a command-looking value", []string{"my-server", "-u", "ls", "--output=json", "ls"}, []string{"my-server", "-u", "ls", "--output=json"}},
		{"unknown flag after the server starts the command", []string{"my-server", "-x", "--output", "json"}, []string{"my-server"}},
		{"read-only boolean value", []string{"my-server", "--read-only", "false", "--output=json"}, []string{"my-server", "--read-only", "false", "--output=json"}},
		{"separator after the server starts the command", []string{"my-server", "--", "--output", "json"}, []string{"my-server"}},
		{"flags before the server", []string{"--output", "json", "my-server", "ls"}, []string{"--output", "json", "my-server"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, append([]string{}, websshOwnArgs(tt.in)...))
		})
	}
}

// Serial: Find builds the merged flag set lazily on package-level commands.
func TestScanArgsResolvesCanonicalCommandNames(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantName string
		wantScan []string
	}{
		{"alias resolves to canonical path", []string{"ws", "ls", "--output", "json"}, "workspace ls", []string{"ws", "ls", "--output", "json"}},
		{"root alone", nil, "", []string{}},
		{"flag before command", []string{"--output", "json", "server", "ls"}, "server ls", []string{"--output", "json", "server", "ls"}},
		{"exec hides remote flags", []string{"exec", "srv", "ls", "--output", "json"}, "exec", []string{}},
		{"exec own flag kept", []string{"exec", "--output", "json", "srv", "ls"}, "exec", []string{"--output", "json"}},
		{"websh flag after the server kept", []string{"websh", "srv", "--output", "json"}, "websh", []string{"srv", "--output", "json"}},
		{"websh remote command flags hidden", []string{"websh", "srv", "ls", "--output", "json"}, "websh", []string{"srv"}},
		{"exec value flag hides an output-looking value", []string{"exec", "--interpreter", "--output=json", "srv", "--", "echo"}, "exec", []string{"--interpreter"}},
		{"websh work-session value that looks like our flag", []string{"websh", "--work-session", "--output=json", "srv"}, "websh", []string{"--work-session", "srv"}},
		{"exec output keeps its own value", []string{"exec", "--output", "json", "srv"}, "exec", []string{"--output", "json"}},
		{"websh value flag hides an output-looking value", []string{"websh", "srv", "-u", "--output=json"}, "websh", []string{"srv", "-u"}},
		{"value of a value flag is not a flag", []string{"event", "wait", "--type", "--output=json", "--timeout", "0"}, "event wait", []string{"event", "wait", "--type", "--timeout"}},
		{"output after a value flag still counts", []string{"event", "wait", "--type", "x", "--output", "json"}, "event wait", []string{"event", "wait", "--type", "--output", "json"}},
		{"unknown flag does not hop", []string{"event", "wait", "--bogus", "--output=json"}, "event wait", []string{"event", "wait", "--bogus", "--output=json"}},
		{"bundle ending in a value flag takes the next token", []string{"cp", "-ru", "--output=json", "onlyone"}, "cp", []string{"cp", "-ru", "onlyone"}},
		{"bundle of bools does not hop", []string{"cp", "-r", "--output", "json", "a", "b"}, "cp", []string{"cp", "-r", "--output", "json", "a", "b"}},
		{"attached shorthand value does not hop", []string{"cp", "-uroot", "--output", "json", "a", "b"}, "cp", []string{"cp", "-uroot", "--output", "json", "a", "b"}},
		{"bundle with attached value does not hop", []string{"cp", "-ruroot", "--output=json"}, "cp", []string{"cp", "-ruroot", "--output=json"}},
		{"shorthand equals form does not hop", []string{"cp", "-u=root", "--output=json"}, "cp", []string{"cp", "-u=root", "--output=json"}},
		{"unknown shorthand in a bundle stops the walk", []string{"cp", "-rzu", "--output=json"}, "cp", []string{"cp", "-rzu", "--output=json"}},
		{"bool flag does not hop", []string{"event", "wait", "--help", "--output=json"}, "event wait", []string{"event", "wait", "--help", "--output=json"}},
		{"long bool flag does not hop", []string{"server", "create", "--json", "--output=json", "--bogus"}, "server create", []string{"server", "create", "--json", "--output=json", "--bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, scan := scanArgs(tt.args)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantScan, append([]string{}, scan...))
		})
	}
}

func runExecuteHelper(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, "TestExecuteJSONHelperProcess", "execute-json-helper", args,
		"GO_WANT_EXECUTE_JSON_HELPER=1", "ALPACON_NO_BROWSER=1", homeEnvVar()+"="+t.TempDir())
}

func TestExecuteJSONReportsCobraErrorsAsOneUsageEnvelope(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantCommand string
		wantMessage string
	}{
		{"unknown flag", []string{"--output", "json", "server", "--bogus"}, "server", "unknown flag: --bogus"},
		{"unknown flag with equals form", []string{"server", "ls", "--output=json", "--bogus"}, "server ls", "unknown flag: --bogus"},
		{"unknown subcommand", []string{"--output", "json", "server", "nope"}, "server", "unknown command"},
		{"group without subcommand", []string{"--output", "json", "server"}, "server", "a subcommand is required"},
		{"alias reports canonical name", []string{"ws", "ls", "--output", "json", "--bogus"}, "workspace ls", "unknown flag: --bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runExecuteHelper(t, tt.args...)

			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			env := lastEnvelopeOfStderr(t, stderr)
			assert.Equal(t, false, env["ok"])
			assert.InDelta(t, 1, env["exit_code"], 0)
			assert.Equal(t, utils.ErrorCodeUsage, env["error_code"])
			assert.Equal(t, tt.wantCommand, env["command"])
			assert.Contains(t, env["message"], tt.wantMessage)
			assert.NotContains(t, env["message"], "While executing the command")
		})
	}
}

// lastEnvelopeOfStderr tolerates help text before the envelope, which is how a group prints.
func lastEnvelopeOfStderr(t *testing.T, stderr string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	var env map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &env), "stderr: %q", stderr)
	return env
}

func TestExecuteJSONSendsHelpToStderr(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"help flag", []string{"--output", "json", "server", "--help"}},
		{"help command", []string{"--output", "json", "help", "server"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runExecuteHelper(t, tt.args...)

			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Usage:")
		})
	}
}

func TestExecuteTableModeKeepsCobraErrorAndHelpOnStdout(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runExecuteHelper(t, "server", "--bogus")

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "Error: unknown flag: --bogus")
	assert.Contains(t, stderr, "While executing the command: unknown flag: --bogus")
	assert.Empty(t, stdout)

	helpOut, _, helpCode := runExecuteHelper(t, "server", "--help")
	assert.Equal(t, 0, helpCode)
	assert.Contains(t, helpOut, "Usage:")
}

func TestExecuteJSONExecAndWebshParseFailuresAreEnvelopes(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"exec unknown flag", []string{"exec", "--output", "json", "--bogus", "srv", "ls"}},
		{"websh missing flag value", []string{"websh", "--output=json", "-u"}},
		{"exec unknown flag with output last", []string{"exec", "--bogus", "--output=json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runExecuteHelper(t, tt.args...)

			assert.NotEqual(t, 0, code)
			assert.Empty(t, stdout)
			env := lastEnvelopeOfStderr(t, stderr)
			assert.Equal(t, false, env["ok"])
			assert.NotEmpty(t, env["error_code"])
		})
	}
}

func TestExecuteJSONWebshFlagsAfterTheServerSelectJSON(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runExecuteHelper(t, "websh", "my-server", "--output", "json", "--read-only=maybe")

	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	env := lastEnvelopeOfStderr(t, stderr)
	assert.Equal(t, false, env["ok"])
	assert.NotEmpty(t, env["error_code"])
	assert.InDelta(t, code, env["exit_code"], 0)
	assert.Equal(t, "websh", env["command"])
}

func TestExecuteEventWaitTypeValueIsNotTakenForOutputFlag(t *testing.T) {
	t.Parallel()
	_, stderr, code := runExecuteHelper(t, "event", "wait", "--type", "--output=json", "--timeout", "0")

	assert.NotEqual(t, 0, code)
	assert.NotContains(t, strings.TrimSpace(stderr), `{"ok":false`)
	assert.Contains(t, stderr, "Error:")
}

func TestExecuteCpBundleValueFlagTakesOutputLookingToken(t *testing.T) {
	t.Parallel()
	_, stderr, code := runExecuteHelper(t, "cp", "-ru", "--output=json", "onlyone")

	assert.NotEqual(t, 0, code)
	assert.NotContains(t, strings.TrimSpace(stderr), `{"ok":false`)
}

func TestExecuteCpOutputAfterBoolBundleSelectsJSON(t *testing.T) {
	t.Parallel()
	_, stderr, code := runExecuteHelper(t, "cp", "-r", "--output", "json", "onlyone")

	assert.NotEqual(t, 0, code)
	assert.Equal(t, false, lastEnvelopeOfStderr(t, stderr)["ok"])
}

func TestExecuteJSONHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXECUTE_JSON_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "execute-json-helper")
	if !ok {
		os.Exit(2)
	}
	os.Args = append([]string{"alpacon"}, args...)
	Execute()
	os.Exit(0)
}
