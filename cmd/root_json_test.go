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

// Serial: Find builds the merged flag set lazily on package-level commands.
func TestScanOutputFormatReadsTheValueTheCommandsParserWillRead(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantName   string
		wantFormat string
	}{
		{"alias resolves to canonical path", []string{"ws", "ls", "--output", "json"}, "workspace ls", utils.OutputFormatJSON},
		{"root alone", nil, "", ""},
		{"flag before command", []string{"--output", "json", "server", "ls"}, "server ls", utils.OutputFormatJSON},
		{"last output wins", []string{"server", "ls", "--output", "json", "--output", "table"}, "server ls", ""},
		{"exec remote command flags are not ours", []string{"exec", "srv", "ls", "--output", "json"}, "exec", ""},
		{"exec own flag", []string{"exec", "--output", "json", "srv", "ls"}, "exec", utils.OutputFormatJSON},
		{"exec flag after an unknown flag", []string{"exec", "--bogus", "--output", "json", "srv"}, "exec", utils.OutputFormatJSON},
		{"exec flag after help", []string{"exec", "-h", "--output", "json"}, "exec", utils.OutputFormatJSON},
		{"exec flag after the separator is the server", []string{"exec", "--", "--output", "json"}, "exec", ""},
		{"exec value flag takes an output-looking value", []string{"exec", "--interpreter", "--output=json", "srv", "--", "echo"}, "exec", ""},
		{"exec attached short value", []string{"exec", "-ualice", "--output", "json", "srv"}, "exec", utils.OutputFormatJSON},
		{"websh flag after the server", []string{"websh", "srv", "--output", "json"}, "websh", utils.OutputFormatJSON},
		{"websh remote command flags are not ours", []string{"websh", "srv", "ls", "--output", "json"}, "websh", ""},
		{"websh prefix flag takes the next token", []string{"websh", "-ualice", "--output", "json", "srv"}, "websh", ""},
		{"websh long prefix flag takes the next token", []string{"websh", "--username-extra", "--output=json", "srv"}, "websh", ""},
		{"websh value flag takes an output-looking value", []string{"websh", "--work-session", "--output=json", "srv"}, "websh", ""},
		{"websh flag after a bad read-only value", []string{"websh", "srv", "--read-only=maybe", "--output", "json"}, "websh", utils.OutputFormatJSON},
		{"websh separator starts the command", []string{"websh", "srv", "--", "--output", "json"}, "websh", ""},
		{"value of a value flag is not a flag", []string{"event", "wait", "--type", "--output=json", "--timeout", "0"}, "event wait", ""},
		{"output after a value flag still counts", []string{"event", "wait", "--type", "x", "--output", "json"}, "event wait", utils.OutputFormatJSON},
		{"unknown flag does not hop", []string{"event", "wait", "--bogus", "--output=json"}, "event wait", utils.OutputFormatJSON},
		{"bundle ending in a value flag takes the next token", []string{"cp", "-ru", "--output=json", "onlyone"}, "cp", ""},
		{"bundle of bools does not hop", []string{"cp", "-r", "--output", "json", "a", "b"}, "cp", utils.OutputFormatJSON},
		{"attached shorthand value does not hop", []string{"cp", "-uroot", "--output", "json", "a", "b"}, "cp", utils.OutputFormatJSON},
		{"bundle with attached value does not hop", []string{"cp", "-ruroot", "--output=json"}, "cp", utils.OutputFormatJSON},
		{"shorthand equals form does not hop", []string{"cp", "-u=root", "--output=json"}, "cp", utils.OutputFormatJSON},
		{"unknown shorthand in a bundle stops the walk", []string{"cp", "-rzu", "--output=json"}, "cp", utils.OutputFormatJSON},
		{"bool flag does not hop", []string{"event", "wait", "--help", "--output=json"}, "event wait", utils.OutputFormatJSON},
		{"long bool flag does not hop", []string{"server", "create", "--json", "--output=json", "--bogus"}, "server create", utils.OutputFormatJSON},
		{"separator ends the scan", []string{"tunnel", "srv", "--", "psql", "--output", "json"}, "tunnel", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, format := scanOutputFormat(tt.args)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantFormat, format)
		})
	}
}

func runExecuteHelper(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, "TestExecuteJSONHelperProcess", "execute-json-helper", args,
		"GO_WANT_EXECUTE_JSON_HELPER=1", "ALPACON_NO_BROWSER=1", homeEnvVar()+"="+t.TempDir())
}

func TestExecuteJSONReportsCobraErrorsAsOneUsageEnvelope(t *testing.T) {
	t.Parallel()
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
		{"earlier table output does not undo the json scan", []string{"server", "ls", "--output", "table", "--bogus", "--output", "json"}, "server ls", "unknown flag: --bogus"},
		{"help command", []string{"help", "--output", "json", "--bogus"}, "help", "unknown flag: --bogus"},
		{"completion command", []string{"completion", "bash", "--output", "json", "--bogus"}, "completion bash", "unknown flag: --bogus"},
		{"unknown help topic", []string{"help", "nosuch", "--output", "json"}, "help", `unknown help topic "nosuch"`},
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
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"help flag", []string{"--output", "json", "server", "--help"}},
		{"help command", []string{"--output", "json", "help", "server"}},
		{"exec help flag", []string{"exec", "--output", "json", "--help"}},
		{"websh help flag", []string{"websh", "--output", "json", "-h"}},
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

func TestExecuteTableModeWritesCobraErrorToStderr(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runExecuteHelper(t, "server", "--bogus")

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "While executing the command: unknown flag: --bogus")
}

func TestExecuteTableModeWritesHelpToStdout(t *testing.T) {
	t.Parallel()
	stdout, _, code := runExecuteHelper(t, "server", "--help")

	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

func TestExecuteJSONExecWebshAndCpArgumentFailuresAreUsageEnvelopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		wantCommand string
		wantMessage string
	}{
		{"exec unknown flag", []string{"exec", "--output", "json", "--bogus", "srv", "uptime"}, "exec", "unknown flag: --bogus"},
		{"exec unknown flag with output last", []string{"exec", "--bogus", "--output=json"}, "exec", "unknown flag: --bogus"},
		{"websh missing flag value", []string{"websh", "--output=json", "--work-session"}, "websh", "--work-session requires a value"},
		{"websh flag after the server", []string{"websh", "my-server", "--output", "json", "--read-only=maybe"}, "websh", "--read-only value must be either"},
		{"exec flag without value", []string{"exec", "--output", "json", "--purpose"}, "exec", "flag needs an argument: --purpose"},
		{"websh empty output keeps the earlier json", []string{"websh", "--output=json", "--output"}, "websh", "--output requires a value"},
		{"websh without a server", []string{"websh", "--output", "json", "-s"}, "websh", "Server name is required"},
		{"cp output after a bool bundle", []string{"cp", "-r", "--output", "json", "onlyone"}, "cp", "at least two arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runExecuteHelper(t, tt.args...)

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Empty(t, stdout)
			env := lastEnvelopeOfStderr(t, stderr)
			assert.Equal(t, utils.ErrorCodeUsage, env["error_code"])
			assert.Equal(t, tt.wantCommand, env["command"])
			assert.Contains(t, env["message"], tt.wantMessage)
		})
	}
}

func TestExecuteTableModeWhenAValueFlagTakesTheOutputToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		wantCode    int
		wantMessage string
	}{
		{"event wait type value", []string{"event", "wait", "--type", "--output=json", "--timeout", "0"}, utils.ExitCodeUsageError, "--timeout must be positive"},
		{"cp bundle ending in a value flag", []string{"cp", "-ru", "--output=json", "onlyone"}, utils.ExitCodeGeneralError, "You must specify at least two arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, stderr, code := runExecuteHelper(t, tt.args...)

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, stderr, tt.wantMessage)
			assert.NotContains(t, stderr, `{"ok":false`)
		})
	}
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
