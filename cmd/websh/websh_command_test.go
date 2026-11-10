package websh

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// stubClient answers every request from respond.
func stubClient(respond func(*http.Request) (status int, body string)) clientFactory {
	return func() (*client.AlpaconClient, error) {
		ac := &client.AlpaconClient{BaseURL: testutil.StubBaseURL, UserAgent: "alpacon-test", HTTPClient: testutil.StubClient(respond)}
		ac.SetAccessToken("secret-token")
		return ac, nil
	}
}

// answer responds to every request with status and a JSON body.
func answer(status int, body string) func(*http.Request) (int, string) {
	return func(*http.Request) (int, string) { return status, body }
}

// noClient stands in for a factory a usage error must never reach.
func noClient() (*client.AlpaconClient, error) { return nil, errors.New("no client in this test") }

// executeWebsh runs the websh command in process under a root, as in the real tree, and prints its
// ExitError the way cmd.Execute does.
func executeWebsh(newClient clientFactory, args ...string) (stderr string, code int) {
	var outBuf, errBuf bytes.Buffer
	command := &cobra.Command{Use: "alpacon"}
	command.AddCommand(newCommand(newClient))
	command.SetArgs(append([]string{"websh"}, args...))
	command.SetOut(&outBuf)
	command.SetErr(&errBuf)
	err := command.Execute()
	if err == nil {
		return errBuf.String(), 0
	}
	exitErr, ok := errors.AsType[*utils.ExitError](err)
	if !ok {
		return err.Error(), -1
	}
	utils.FprintExitError(&errBuf, exitErr)
	return errBuf.String(), exitErr.Code
}

func TestWebshRefusesABadCommandLineWithExit2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "no server", args: []string{}, wantStderr: "Server name is required."},
		{name: "share with a remote command", args: []string{"--share", "my-server", "whoami"}, wantStderr: "The --share flag cannot be used with remote commands."},
		{name: "unknown flag", args: []string{"--bogus", "my-server"}, wantStderr: "unknown flag: --bogus\nRun 'alpacon websh --help' for usage."},
		{name: "flag without its value", args: []string{"--work-session"}, wantStderr: "--work-session requires a value"},
		{name: "invite without an email", args: []string{"invite", "550e8400-e29b-41d4-a716-446655440000"}, wantStderr: "At least one --email is required."},
		{name: "ls with a zero tail", args: []string{"ls", "--tail", "0"}, wantStderr: "--tail must be a positive integer."},
		{name: "records with a zero limit", args: []string{"records", "550e8400-e29b-41d4-a716-446655440000", "--limit", "0"}, wantStderr: "--limit must be a positive integer."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stderr, code := executeWebsh(noClient, tt.args...)

			assert.Equal(t, utils.ExitCodeUsageError, code)
			assert.Contains(t, stderr, tt.wantStderr)
		})
	}
}

func TestWebshSubcommandsReportAClientThatCannotBeBuilt(t *testing.T) {
	t.Parallel()
	const id = "550e8400-e29b-41d4-a716-446655440000"
	tests := []struct {
		name string
		args []string
	}{
		{name: "ls", args: []string{"ls"}},
		{name: "describe", args: []string{"describe", id}},
		{name: "close", args: []string{"close", id}},
		{name: "force-close", args: []string{"force-close", id}},
		{name: "invite", args: []string{"invite", id, "--email", "alice@example.com"}},
		{name: "join", args: []string{"join", "--url", "https://myws.us1.alpacon.io/websh/shared/abcd1234", "--password", "my-session-pass"}},
		{name: "watch", args: []string{"watch", id}},
		{name: "records", args: []string{"records", id}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stderr, code := executeWebsh(noClient, tt.args...)

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, stderr, "Connection to Alpacon API failed: no client in this test. Consider re-logging.\n")
			assert.Contains(t, stderr, "For issues, check the latest version or report on")
		})
	}
}

func TestWebshCloseReportsTheServersRefusalWithExit1(t *testing.T) {
	t.Parallel()

	stderr, code := executeWebsh(stubClient(answer(http.StatusInternalServerError, `{"detail": "boom"}`)), "close", "550e8400-e29b-41d4-a716-446655440000")

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, stderr, "Failed to close websh session:")
}

// Serial: HOME points at an empty directory so the auth line reads no real config.
func TestWebshSessionRefusedByTheWorkSessionGateExits3WithTheDiagnostic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALPACON_WORK_SESSION", "")

	stderr, code := executeWebsh(stubClient(answer(http.StatusForbidden, `{"code": "work_session_required"}`)),
		"--work-session", "550e8400-e29b-41d4-a716-446655440000", "my-server")

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, code)
	assert.Contains(t, stderr, "the websh operation requires an active WorkSession on this authentication.")
	assert.Contains(t, stderr, "  target server : my-server\n")
}

// Serial: HOME points at an empty directory, and the denial banner goes to the process stderr.
func TestWebshCommandRefusedByTheWorkSessionGateExits3WithTheDiagnostic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALPACON_WORK_SESSION", "")
	newClient := stubClient(func(req *http.Request) (int, string) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/servers/servers/":
			return http.StatusOK, `{"count":1,"results":[{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","name":"my-server"}]}`
		case req.Method == http.MethodPost && req.URL.Path == "/api/events/commands/":
			return http.StatusForbidden, `{"code": "work_session_required", "source": "command"}`
		default: // the output stream is refused, so the run falls back to polling at once
			return http.StatusNotFound, `{}`
		}
	})

	var stderr string
	var code int
	_, banner := testutil.CaptureOutput(t, func() {
		stderr, code = executeWebsh(newClient, "--work-session", "550e8400-e29b-41d4-a716-446655440000", "my-server", "whoami")
	})

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, code)
	assert.Contains(t, stderr, "  target server : my-server\n")
	assert.Contains(t, banner, "whoami on my-server was refused by the work_session gate and did not run.")
}
