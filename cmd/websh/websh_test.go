package websh

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandParsing(t *testing.T) {
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

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "server returns Not found.",
			err:      fmt.Errorf("Not found."),
			expected: true,
		},
		{
			name:     "exact not found without period",
			err:      fmt.Errorf("Not found"),
			expected: true,
		},
		{
			name:     "case insensitive NOT FOUND",
			err:      fmt.Errorf("NOT FOUND"),
			expected: true,
		},
		{
			name:     "wrapped not found error",
			err:      fmt.Errorf("failed to create event session: Not found"),
			expected: true,
		},
		{
			name:     "wrapped with period",
			err:      fmt.Errorf("failed to subscribe: Not found."),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      fmt.Errorf("connection refused"),
			expected: false,
		},
		{
			name:     "api insufficient data",
			err:      fmt.Errorf("code: api_insufficient_data"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isNotFoundError(tt.err))
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
	got, err := ParseWebshArgs([]string{"--work-session", "ses-abc", "my-server"})
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got.WorkSessionID)
	assert.Equal(t, "my-server", got.ServerName)
}

func TestParseWebshArgs_WorkSessionEqualForm(t *testing.T) {
	got, err := ParseWebshArgs([]string{"--work-session=ses-abc", "my-server"})
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got.WorkSessionID)
	assert.Equal(t, "my-server", got.ServerName)
}

func TestWebshReRunHint(t *testing.T) {
	t.Run("minimal: server and command only", func(t *testing.T) {
		got := webshReRunHint(WebshArgs{ServerName: "web-01"}, "sudo reboot")
		assert.Equal(t, "alpacon websh web-01 sudo reboot", got)
	})

	t.Run("username only", func(t *testing.T) {
		got := webshReRunHint(WebshArgs{Username: "admin", ServerName: "web-01"}, "uptime")
		assert.Equal(t, "alpacon websh -u admin web-01 uptime", got)
	})

	t.Run("groupname only", func(t *testing.T) {
		got := webshReRunHint(WebshArgs{Groupname: "sysadmin", ServerName: "web-01"}, "uptime")
		assert.Equal(t, "alpacon websh -g sysadmin web-01 uptime", got)
	})

	t.Run("includes user, group, and work-session", func(t *testing.T) {
		got := webshReRunHint(WebshArgs{
			Username:      "root",
			Groupname:     "docker",
			WorkSessionID: "ses-1",
			ServerName:    "web-01",
		}, "sudo reboot")
		assert.Equal(t, "alpacon websh -u root -g docker --work-session ses-1 web-01 sudo reboot", got)
	})
}

func TestParseWebshArgs_CommandAfterServerNotConsumed(t *testing.T) {
	got, err := ParseWebshArgs([]string{"my-server", "ls", "--work-session", "fake"})
	require.NoError(t, err)
	assert.Equal(t, "my-server", got.ServerName)
	assert.Equal(t, "", got.WorkSessionID)
	assert.Equal(t, []string{"ls", "--work-session", "fake"}, got.CommandArgs)
}

// newWebshApprovalDenialServer returns a test server that resolves one server
// and always answers a command with a SUDO_APPROVAL_REQUIRED denial (success:
// false + the plugin denial line), so the command stays pending. Mirrors
// cmd/exec/pending_approval_test.go's newApprovalDenialServer: websh's
// non-interactive command path shares RunCommandWithRetry with exec, so it
// exercises the same submit/poll endpoints.
func newWebshApprovalDenialServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			resp := map[string]any{
				"id":          "cmd-1",
				"status":      "completed",
				"success":     false,
				"exit_code":   1,
				"result":      "Alpacon denied this sudo command (SUDO_APPROVAL_REQUIRED).\n",
				"error_phase": nil,
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

// newWebshAwaitingApprovalServer returns a test server whose command parks at
// the server-side "awaiting_approval" status (HITL hold), so the command is
// pending human approval without ever producing a denial line.
func newWebshAwaitingApprovalServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmd-1",
				"status": "awaiting_approval",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// writeWebshCommandTestConfig mirrors cmd/exec/worksession_command_test.go's
// writeExecCommandTestConfig; each package owns its own copy since the helper
// is unexported there.
func writeWebshCommandTestConfig(t *testing.T, home, workspaceURL string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".alpacon")
	require.NoError(t, os.MkdirAll(cfgDir, 0700))

	cfg := map[string]any{
		"workspace_url":           workspaceURL,
		"workspace_name":          "test",
		"access_token":            "access-token",
		"refresh_token":           "refresh-token",
		"access_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"active_work_sessions":    map[string]string{},
		"insecure":                false,
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600))
}

// webshHelperArgs finds the "websh-command-helper" marker in os.Args and
// returns everything after it, mirroring execWorkSessionHelperArgs.
func webshHelperArgs(args []string) ([]string, bool) {
	for i := range args {
		if args[i] == "websh-command-helper" {
			return args[i+1:], true
		}
	}
	return nil, false
}

// TestWebshCommandHelperProcess is re-invoked as a subprocess (via os.Args[0])
// by the tests below; it is a no-op under a normal `go test` run.
func TestWebshCommandHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WEBSH_HELPER") != "1" {
		return
	}

	args, ok := webshHelperArgs(os.Args)
	if !ok {
		fmt.Fprintln(os.Stderr, "missing websh-command-helper marker")
		os.Exit(2)
	}
	WebshCmd.Run(WebshCmd, args)
}

// TestWebshCommandPendingApprovalExits4WithJSONSignal drives a sudo command
// denied SUDO_APPROVAL_REQUIRED through the real websh non-interactive command
// path and asserts it converges on the same pending-approval contract as exec:
// exit 4 and a {"status":"pending_approval", ...} envelope, with a websh-shaped
// re-run hint rather than exec's (AC4).
func TestWebshCommandPendingApprovalExits4WithJSONSignal(t *testing.T) {
	ts := newWebshApprovalDenialServer()
	defer ts.Close()

	home := t.TempDir()
	writeWebshCommandTestConfig(t, home, ts.URL)

	helper := osexec.Command(
		os.Args[0],
		"-test.run=^TestWebshCommandHelperProcess$",
		"--",
		"websh-command-helper",
		"--output",
		"json",
		"prod",
		"sudo",
		"reboot",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_WEBSH_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T", err)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.ExitCode(), "pending approval must exit 4")

	var got struct {
		OK          bool     `json:"ok"`
		Status      string   `json:"status"`
		ExitCode    int      `json:"exit_code"`
		NextActions []string `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon websh prod sudo reboot", got.NextActions[0], "re-run hint should reconstruct the websh invocation, not exec's")
}

// TestWebshCommandStatusAwaitingApprovalExits4WithJSONSignal drives the
// status-level HITL hold (server status "awaiting_approval", no denial line)
// through the real websh command and asserts it converges on the same
// pending-approval contract, with next_actions pointing at exec logs—lookup is
// job-ID based, so it has no websh-specific form (AC5).
func TestWebshCommandStatusAwaitingApprovalExits4WithJSONSignal(t *testing.T) {
	ts := newWebshAwaitingApprovalServer()
	defer ts.Close()

	home := t.TempDir()
	writeWebshCommandTestConfig(t, home, ts.URL)

	helper := osexec.Command(
		os.Args[0],
		"-test.run=^TestWebshCommandHelperProcess$",
		"--",
		"websh-command-helper",
		"--output",
		"json",
		"prod",
		"sudo",
		"reboot",
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_WEBSH_HELPER=1",
		"ALPACON_WORK_SESSION=",
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T", err)
	assert.Equal(t, utils.ExitCodePendingApproval, exitErr.ExitCode(), "pending approval must exit 4")

	var got struct {
		OK          bool     `json:"ok"`
		Status      string   `json:"status"`
		ExitCode    int      `json:"exit_code"`
		NextActions []string `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec logs cmd-1", got.NextActions[0], "status-hold hint should point at exec logs regardless of caller")
}
