package exec

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const failedJobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"

// newFailedCommandServer answers every command read with detail, a command that already ended.
func newFailedCommandServer(t *testing.T, detail map[string]any) *httptest.Server {
	t.Helper()
	detail = maps.Clone(detail)
	detail["id"] = failedJobID
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"` + failedJobID + `"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+failedJobID+"/":
			_ = json.NewEncoder(w).Encode(detail)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func lastStderrEnvelope(t *testing.T, stderr string) utils.JSONErrorEnvelope[map[string]any] {
	t.Helper()
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	var env utils.JSONErrorEnvelope[map[string]any]
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &env), "stderr: %q", stderr)
	return env
}

func TestRemoteCommandFailureEndsWithAnEnvelopeUnderJSON(t *testing.T) {
	t.Parallel()
	exited := map[string]any{"status": "completed", "success": false, "exit_code": 3, "result": "boom\n"}
	noExitCode := map[string]any{"status": "completed", "success": false, "result": "boom\n"}
	phased := map[string]any{"status": "completed", "success": false, "exit_code": 126, "error_phase": "permission_denied", "result": ""}
	tests := []struct {
		name         string
		detail       map[string]any
		logs         bool
		wantExit     int
		wantRemote   bool
		wantMessage  string
		wantNoStdout bool
	}{
		{name: "exec with an exit code", detail: exited, wantExit: 3, wantRemote: true, wantMessage: "exited with code 3"},
		{name: "exec with a phase", detail: phased, wantExit: 126, wantRemote: true, wantMessage: "[permission_denied]", wantNoStdout: true},
		{name: "logs with an exit code", detail: exited, logs: true, wantExit: 3, wantRemote: true, wantMessage: "exited with code 3"},
		{name: "logs without an exit code", detail: noExitCode, logs: true, wantExit: 1, wantMessage: "exited with code 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newFailedCommandServer(t, tt.detail)

			var stdout, stderr string
			var exitCode int
			if tt.logs {
				stdout, stderr, exitCode = runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, failedJobID)
			} else {
				stdout, stderr, exitCode = runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "false")
			}

			assert.Equal(t, tt.wantExit, exitCode, "stderr: %s", stderr)
			if !tt.wantNoStdout {
				assert.Contains(t, stdout, "boom")
			}
			env := lastStderrEnvelope(t, stderr)
			assert.Equal(t, tt.wantExit, env.ExitCode)
			assert.Equal(t, utils.ErrorCodeRemoteCommandFailed, env.ErrorCode)
			assert.Contains(t, env.Message, tt.wantMessage)
			assert.NotContains(t, env.Message, "\x1b")
			if tt.wantRemote {
				require.NotNil(t, env.RemoteExitCode)
				assert.Equal(t, tt.wantExit, *env.RemoteExitCode)
			} else {
				assert.Nil(t, env.RemoteExitCode)
			}
		})
	}
}

func TestRemoteCommandFailureKeepsTableOutput(t *testing.T) {
	t.Parallel()
	ts := newFailedCommandServer(t, map[string]any{"status": "completed", "success": false, "exit_code": 126, "error_phase": "permission_denied"})

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "false")

	assert.Equal(t, 126, exitCode)
	assert.Contains(t, stderr, "[permission_denied]")
	assert.NotContains(t, stderr, `"error_code"`)
}

func TestClientTimeoutEndsWithAnEnvelopeUnderJSON(t *testing.T) {
	t.Parallel()
	helper := osexec.Command(os.Args[0], "-test.run=^TestClientTimeoutHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_CLIENT_TIMEOUT_HELPER=1")
	var stdout, stderr bytes.Buffer
	helper.Stdout, helper.Stderr = &stdout, &stderr

	err := helper.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.ExitCode())
	assert.Empty(t, stdout.String())
	env := lastStderrEnvelope(t, stderr.String())
	assert.Equal(t, utils.ExitCodeGeneralError, env.ExitCode)
	assert.Equal(t, utils.ErrorCodeCLI, env.ErrorCode)
	assert.Contains(t, env.Message, "[client_timeout]")
}

// Serial: the child reaches an os.Exit.
func TestClientTimeoutHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CLIENT_TIMEOUT_HELPER") == "" {
		return
	}
	utils.OutputFormat = utils.OutputFormatJSON
	HandleCommandResult(&event.ClientTimeoutError{}, ExecInvocation)
}
