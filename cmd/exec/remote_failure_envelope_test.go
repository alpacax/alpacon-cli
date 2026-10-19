package exec

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const remoteFailureJobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"

type failureEnvelope struct {
	OK             bool   `json:"ok"`
	ExitCode       int    `json:"exit_code"`
	ErrorCode      string `json:"error_code"`
	Message        string `json:"message"`
	RemoteExitCode *int   `json:"remote_exit_code"`
}

func lastStderrEnvelope(t *testing.T, stderr string) failureEnvelope {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	var env failureEnvelope
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &env), "stderr: %s", stderr)
	return env
}

func newRemoteFailureServer(detail map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":%q}]`, remoteFailureJobID)
		case r.URL.Path == "/api/events/commands/"+remoteFailureJobID+"/":
			_ = json.NewEncoder(w).Encode(detail)
		default:
			http.NotFound(w, r)
		}
	}))
}

func failedDetail(extra map[string]any) map[string]any {
	detail := map[string]any{
		"id": remoteFailureJobID, "status": "completed", "success": false,
		"exit_code": 3, "result": "boom\n", "error_phase": nil,
	}
	for k, v := range extra {
		detail[k] = v
	}
	return detail
}

func TestExecRemoteFailureEndsWithAnEnvelopeUnderJSON(t *testing.T) {
	t.Parallel()
	ts := newRemoteFailureServer(failedDetail(nil))
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "false")

	assert.Equal(t, 3, exitCode)
	assert.Contains(t, stdout, "boom")
	env := lastStderrEnvelope(t, stderr)
	assert.False(t, env.OK)
	assert.Equal(t, 3, env.ExitCode)
	assert.Equal(t, utils.ErrorCodeRemoteCommandFailed, env.ErrorCode)
	require.NotNil(t, env.RemoteExitCode)
	assert.Equal(t, 3, *env.RemoteExitCode)
}

func TestExecRemoteFailureTableModePrintsNoEnvelope(t *testing.T) {
	t.Parallel()
	ts := newRemoteFailureServer(failedDetail(nil))
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "false")

	assert.Equal(t, 3, exitCode)
	assert.Contains(t, stdout, "boom")
	assert.NotContains(t, stderr, "error_code")
}

func TestExecLogsRemoteFailureEndsWithAnEnvelopeUnderJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		detail     map[string]any
		wantExit   int
		wantCode   string
		wantRemote *int
	}{
		{"remote exit code", failedDetail(nil), 3, utils.ErrorCodeRemoteCommandFailed, new(3)},
		{"failure without exit code", failedDetail(map[string]any{"exit_code": nil}), 1, utils.ErrorCodeRemoteCommandFailed, nil},
		{"status only failure", map[string]any{"id": remoteFailureJobID, "status": "stuck"}, 1, utils.ErrorCodeRemoteCommandFailed, nil},
		{"unrecognised status", map[string]any{"id": remoteFailureJobID, "status": "weird"}, 1, utils.ErrorCodeCLI, nil},
	}
	for _, tt := range tests {
		t.Run(strings.ReplaceAll(tt.name, " ", "_"), func(t *testing.T) {
			t.Parallel()
			ts := newRemoteFailureServer(tt.detail)
			defer ts.Close()

			_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, remoteFailureJobID)

			assert.Equal(t, tt.wantExit, exitCode, "stderr: %s", stderr)
			env := lastStderrEnvelope(t, stderr)
			assert.Equal(t, tt.wantExit, env.ExitCode)
			assert.Equal(t, tt.wantCode, env.ErrorCode)
			assert.Equal(t, tt.wantRemote, env.RemoteExitCode)
		})
	}
}

func TestRemoteFailureFoldsPhaseAndHintIntoTheMessage(t *testing.T) {
	t.Parallel()
	remote := 7

	f := remoteFailure("", "", "", 7, &remote)
	assert.Equal(t, "the remote command exited with code 7", f.Message)

	f = remoteFailure("client_timeout", "", "Hint: try again\n", 7, &remote)
	assert.Contains(t, f.Message, "[client_timeout]")
	assert.Contains(t, f.Message, "Hint: try again")
	assert.Equal(t, utils.ErrorCodeRemoteCommandFailed, f.ErrorCode)
}
