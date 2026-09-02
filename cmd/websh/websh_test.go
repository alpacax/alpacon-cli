package websh

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	execCmd "github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusErr carries an HTTP status the way client's own errors do, so the warning
// decision can be tested without standing up a server.
type statusErr struct {
	status int
}

func (e statusErr) Error() string       { return fmt.Sprintf("server said %d", e.status) }
func (e statusErr) HTTPStatusCode() int { return e.status }

func TestCommandParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		testName          string
		args              []string
		expectUsername    string
		expectGroupname   string
		expectServerName  string
		expectEnv         map[string]string
		expectCommandArgs []string
		expectShare       bool
		expectReadOnly    bool
	}{
		{
			testName:          "ExecuteUpdateAsAdminSysadmin",
			args:              []string{"-u", "admin", "-g", "sysadmin", "update-server", "sudo", "apt-get", "update"},
			expectUsername:    "admin",
			expectGroupname:   "sysadmin",
			expectEnv:         map[string]string{},
			expectServerName:  "update-server",
			expectCommandArgs: []string{"sudo", "apt-get", "update"},
		},
		{
			testName:          "DockerComposeDeploymentWithFlags",
			args:              []string{"deploy-server", "docker-compose", "-f", "/home/admin/deploy/docker-compose.yml", "up", "-d"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "deploy-server",
			expectCommandArgs: []string{"docker-compose", "-f", "/home/admin/deploy/docker-compose.yml", "up", "-d"},
		},
		{
			testName:          "VerboseListInFileServer",
			args:              []string{"file-server", "ls", "-l", "/var/www"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "file-server",
			expectCommandArgs: []string{"ls", "-l", "/var/www"},
		},
		{
			testName:          "UnrecognizedFlagWithEchoCommand",
			args:              []string{"-x", "unknown-server", "echo", "Hello World"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "-x",
			expectCommandArgs: []string{"unknown-server", "echo", "Hello World"},
		},
		{
			testName:          "AdminSysadminAccessToMultiFlagServer",
			args:              []string{"--username=admin", "--groupname=sysadmin", "multi-flag-server", "uptime"},
			expectUsername:    "admin",
			expectGroupname:   "sysadmin",
			expectEnv:         map[string]string{},
			expectServerName:  "multi-flag-server",
			expectCommandArgs: []string{"uptime"},
		},
		{
			testName:          "CommandLineArgsResembleFlags",
			args:              []string{"--username", "admin", "server-name", "--fake-flag", "value"},
			expectUsername:    "admin",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"--fake-flag", "value"},
		},
		{
			testName:          "SysadminGroupWithMixedSyntax",
			args:              []string{"-g=sysadmin", "server-name", "echo", "hello world"},
			expectUsername:    "",
			expectGroupname:   "sysadmin",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"echo", "hello world"},
		},
		{
			testName:          "HelpRequestedViaCombinedFlags",
			args:              []string{"-rh"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "-rh",
			expectCommandArgs: nil,
		},
		{
			testName:          "InvalidUsageDetected",
			args:              []string{"-u", "user", "-x", "unknown-flag", "server-name", "cmd"},
			expectUsername:    "user",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "-x",
			expectCommandArgs: []string{"unknown-flag", "server-name", "cmd"},
		},
		{
			testName:          "ValidFlagsFollowedByInvalidFlag",
			args:              []string{"-u", "user", "-g", "group", "-x", "server-name", "cmd"},
			expectUsername:    "user",
			expectGroupname:   "group",
			expectEnv:         map[string]string{},
			expectServerName:  "-x",
			expectCommandArgs: []string{"server-name", "cmd"},
		},
		{
			testName:          "FlagsIntermixedWithCommandArgs",
			args:              []string{"server-name", "-u", "user", "cmd", "-g", "group"},
			expectUsername:    "user",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd", "-g", "group"},
		},
		{
			testName:          "FlagsAndCommandArgsIntertwined",
			args:              []string{"server-name", "-u", "user", "cmd", "-g", "group"},
			expectUsername:    "user",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd", "-g", "group"},
		},
		{
			testName:          "ShareSessionWithFlags",
			args:              []string{"--share", "test-server"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "test-server",
			expectCommandArgs: nil,
			expectShare:       true,
			expectReadOnly:    false,
		},
		{
			testName:          "ReadOnlySharedSession",
			args:              []string{"--share", "--read-only=true", "test-server"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "test-server",
			expectCommandArgs: nil,
			expectShare:       true,
			expectReadOnly:    true,
		},
		{
			testName:          "ReadOnlySharedSession2",
			args:              []string{"--share", "--read-only=True", "test-server"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "test-server",
			expectCommandArgs: nil,
			expectShare:       true,
			expectReadOnly:    true,
		},
		{
			testName:          "SingleEnvVariable",
			args:              []string{"--env=KEY1=value1", "server-name", "cmd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{"KEY1": "value1"},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd"},
		},
		{
			testName:          "MultipleEnvVariables",
			args:              []string{"--env=KEY1=value1", "--env=KEY2=value2", "server-name", "cmd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{"KEY1": "value1", "KEY2": "value2"},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd"},
		},
		{
			testName:          "EnvKeyWithoutValue",
			args:              []string{"--env=KEY", "server-name", "cmd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"cmd"},
		},
		{
			testName:          "ShellCommand",
			args:              []string{"server-name", "ls; cat /etc/passwd"},
			expectUsername:    "",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "server-name",
			expectCommandArgs: []string{"ls; cat /etc/passwd"},
		},
		{
			testName:          "UserAtHostSyntax",
			args:              []string{"root@prod-docker"},
			expectUsername:    "root",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "prod-docker",
			expectCommandArgs: nil,
		},
		{
			testName:          "UserAtHostWithCommand",
			args:              []string{"admin@web-server", "docker", "ps"},
			expectUsername:    "admin",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "web-server",
			expectCommandArgs: []string{"docker", "ps"},
		},
		{
			testName:          "UserFlagOverridesUserAtHost",
			args:              []string{"-u", "override", "root@prod-docker", "ls"},
			expectUsername:    "override",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "prod-docker",
			expectCommandArgs: []string{"ls"},
		},
		{
			testName:          "ComplexHostnameWithUser",
			args:              []string{"deploy@web-server-01.example.com", "uptime"},
			expectUsername:    "deploy",
			expectGroupname:   "",
			expectEnv:         map[string]string{},
			expectServerName:  "web-server-01.example.com",
			expectCommandArgs: []string{"uptime"},
		},
		{
			testName:          "DashSAfterCommandNotParsedAsShare",
			args:              []string{"my-server", "ls", "-s"},
			expectServerName:  "my-server",
			expectEnv:         map[string]string{},
			expectCommandArgs: []string{"ls", "-s"},
			expectShare:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.testName, func(t *testing.T) {
			username, groupname, serverName, commandArgs, share, readOnly, env := executeTestCommand(t, tc.args)

			assert.Equal(t, tc.expectUsername, username, "Mismatch in username")
			assert.Equal(t, tc.expectGroupname, groupname, "Mismatch in groupname")
			assert.Equal(t, tc.expectServerName, serverName, "Mismatch in server name")
			assert.Equal(t, tc.expectCommandArgs, commandArgs, "Mismatch in command arguments")
			assert.Equal(t, tc.expectShare, share, "Mismatch in share flag")
			assert.Equal(t, tc.expectReadOnly, readOnly, "Mismatch in read-only flag")
			assert.Equal(t, tc.expectEnv, env, "Mismatch in env")
		})
	}
}

func TestSudoListenerWarning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cause error
		want  string
	}{
		{
			name:  "an older server without the events API stays silent",
			cause: fmt.Errorf("failed to create event session: %w", statusErr{status: http.StatusNotFound}),
			want:  "",
		},
		{
			name:  "a rejected request names the server's reason",
			cause: fmt.Errorf("failed to create event session: %w", statusErr{status: http.StatusBadRequest}),
			want:  "Sudo MFA listener unavailable: failed to create event session: server said 400",
		},
		{
			name:  "no cause at all means the wait ran out",
			cause: nil,
			want:  "Sudo MFA listener unavailable: timed out connecting to the event channel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sudoListenerWarning(tt.cause))
		})
	}
}

func executeTestCommand(t *testing.T, args []string) (string, string, string, []string, bool, bool, map[string]string) {
	t.Helper()
	parsed, err := ParseWebshArgs(args)
	if errors.Is(err, errHelpRequested) {
		return parsed.Username, parsed.Groupname, parsed.ServerName, parsed.CommandArgs,
			parsed.Share, parsed.ReadOnly, parsed.Env
	}
	require.NoError(t, err)

	username := parsed.Username
	serverName := parsed.ServerName

	// Parse SSH-like syntax for user@host (same logic as in the main command)
	if strings.Contains(serverName, "@") && !strings.Contains(serverName, ":") {
		sshTarget := utils.ParseSSHTarget(serverName)
		if username == "" && sshTarget.User != "" {
			username = sshTarget.User
		}
		serverName = sshTarget.Host
	}

	return username, parsed.Groupname, serverName, parsed.CommandArgs, parsed.Share, parsed.ReadOnly, parsed.Env
}

func TestParseWebshArgs_WorkSessionFlag(t *testing.T) {
	t.Parallel()
	got, err := ParseWebshArgs([]string{"--work-session", "ses-abc", "my-server"})
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got.WorkSessionID)
	assert.Equal(t, "my-server", got.ServerName)
}

func TestParseWebshArgs_WorkSessionEqualForm(t *testing.T) {
	t.Parallel()
	got, err := ParseWebshArgs([]string{"--work-session=ses-abc", "my-server"})
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got.WorkSessionID)
	assert.Equal(t, "my-server", got.ServerName)
}

func TestParseWebshArgs_EnvErrorHidesSecret(t *testing.T) {
	t.Parallel()
	_, err := ParseWebshArgs([]string{"--env=\"=hunter2\"", "my-server", "ls"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2", "malformed --env error must not echo the value")
}

func TestParseWebshArgs_EnvPrefixIsExact(t *testing.T) {
	t.Parallel()
	// --env-file must not be swallowed by --env matching; websh has no
	// unknown-flag rejection, so it falls through to the server-name slot.
	got, err := ParseWebshArgs([]string{"--env-file=secrets", "ls"})
	require.NoError(t, err)
	assert.Equal(t, "--env-file=secrets", got.ServerName)
}

func TestParseWebshArgs_CommandAfterServerNotConsumed(t *testing.T) {
	t.Parallel()
	got, err := ParseWebshArgs([]string{"my-server", "ls", "--work-session", "fake"})
	require.NoError(t, err)
	assert.Equal(t, "my-server", got.ServerName)
	assert.Equal(t, "", got.WorkSessionID)
	assert.Equal(t, []string{"ls", "--work-session", "fake"}, got.CommandArgs)
}

func TestSanitizeRecord(t *testing.T) {
	t.Parallel()
	// ANSI color codes stripped, newlines collapsed to single spaces.
	in := "\x1b[31mdocker\x1b[0m ps\n  -a\tfoo"
	assert.Equal(t, "docker ps -a foo", sanitizeRecord(in, 100))

	// Truncation applies after cleaning.
	assert.Equal(t, "docke...", sanitizeRecord("docker ps", 5))

	// OSC window-title sequences are stripped, not leaked into output.
	assert.Equal(t, "bar", sanitizeRecord("\x1b]0;t\x07bar", 100))

	// A control char removed from between two spaces leaves no double space.
	assert.Equal(t, "foo bar", sanitizeRecord("foo \x07 bar", 100))

	// C1 control (U+0085) separates tokens rather than leaking to the terminal.
	assert.Equal(t, "foo bar", sanitizeRecord("foo"+string(rune(0x85))+"bar", 100))

	// A bidi override (U+202E) carries no control byte, so IsControlRune alone
	// leaves it free to reorder the record the reviewer reads. It is zero-width,
	// so it goes without leaving a space behind.
	assert.Equal(t, "foobar", sanitizeRecord("foo\u202ebar", 100))

	// A format char buried in a sequence would break the escape match, leaving
	// the sequence's tail on screen as text.
	assert.Equal(t, "ls", sanitizeRecord("\x1b[2\u200dKls", 100))
}

func TestPrintWatchHeader_StripsControlSequences(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printWatchHeader(&buf, websh.SessionDetailResponse{
		ID:       "abc123",
		Server:   types.ServerSummary{Name: "web-01\x1b[2K\rdb-prod"},
		User:     types.UserSummary{Name: "Alice\nUser: Bob"},
		Username: "alice\x1b[1A",
	})

	got := buf.String()
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\r")
	// Four fields, the read-only notice, and the three blank lines around them.
	assert.Equal(t, 8, strings.Count(got, "\n"), "a newline must not forge an identity line")
	assert.Contains(t, got, "Server:   web-01db-prod\n")
	assert.Contains(t, got, "Username: alice\n")
}

// TestBuildRemoteExecArgsPinsWebshInvocation pins InvokedAs to WebshInvocation:
// the one line that makes a refusal hint render websh syntax instead of exec's.
// Without it every field still flows and the whole suite stays green, so this
// is the only guard against a refactor dropping the field.
func TestBuildRemoteExecArgsPinsWebshInvocation(t *testing.T) {
	parsed := WebshArgs{
		Groupname:     "devs",
		WorkSessionID: "ws-1",
		OutputFormat:  "json",
		CommandArgs:   []string{"psql", "-h", "localhost"},
		Env:           map[string]string{"PGPASSWORD": "x"},
	}

	args := buildRemoteExecArgs(parsed, "root", "prod")

	assert.Equal(t, execCmd.WebshInvocation, args.InvokedAs)
	assert.Equal(t, "root", args.Username)
	assert.Equal(t, "devs", args.Groupname)
	assert.Equal(t, "ws-1", args.WorkSessionID)
	assert.Equal(t, "json", args.OutputFormat)
	assert.Equal(t, "prod", args.Server)
	assert.Equal(t, "psql -h localhost", args.Command)
	assert.Equal(t, parsed.Env, args.Env)
}

// newSudoListenerTestServer serves only the event session endpoint, with the status
// and body the test wants, so setupSudoListener's failure path can be driven directly.
func newSudoListenerTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/events/sessions/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestSetupSudoListener_StaysSilentOnNotFound(t *testing.T) {
	ts := newSudoListenerTestServer(t, http.StatusNotFound, `{"detail":"Not found."}`)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	var listener *event.SudoListener
	_, stderr := testutil.CaptureOutput(t, func() {
		listener = setupSudoListener(ac, "my-server", "session-1")
	})

	assert.Nil(t, listener)
	assert.Empty(t, stderr, "an older server without the events API must not warn")
}

func TestSetupSudoListener_WarnsOnOtherFailures(t *testing.T) {
	ts := newSudoListenerTestServer(t, http.StatusBadRequest, `{"detail":"Invalid input."}`)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	var listener *event.SudoListener
	_, stderr := testutil.CaptureOutput(t, func() {
		listener = setupSudoListener(ac, "my-server", "session-1")
	})

	assert.Nil(t, listener)
	assert.Contains(t, stderr, "Sudo MFA listener unavailable")
}

func TestSudoListenerWarning_SanitizesTheServersText(t *testing.T) {
	t.Parallel()
	warning := sudoListenerWarning(errors.New("boom \x1b[31mred\x1b[0m"))

	assert.NotContains(t, warning, "\x1b")
	assert.Contains(t, warning, "boom")
}

func TestSetupSudoListener_WarningReturnsTheCursor(t *testing.T) {
	ts := newSudoListenerTestServer(t, http.StatusBadRequest, `{"detail":"Invalid input."}`)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, stderr := testutil.CaptureOutput(t, func() {
		_ = setupSudoListener(ac, "my-server", "session-1")
	})

	require.NotEmpty(t, stderr, "a rejected request must warn, or there is nothing to check")
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		assert.True(t, strings.HasSuffix(line, "\r"),
			"websh may already hold the terminal in raw mode, where a bare newline leaves the cursor mid-line: %q", line)
	}
}
