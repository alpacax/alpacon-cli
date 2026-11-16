package tunnel

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const issueFooter = "For issues, check the latest version or report on"

// runTunnelCommand runs a fresh tunnel command and returns its exit code and what cmd.Execute would print.
func runTunnelCommand(t *testing.T, newClient func() (*client.AlpaconClient, error), args ...string) (int, string) {
	t.Helper()
	cmd := newCommand(newClient)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.Execute()
	if err == nil {
		return 0, out.String()
	}
	exitErr, ok := errors.AsType[*utils.ExitError](err)
	require.True(t, ok, "want an ExitError, got %v", err)
	utils.FprintExitError(&out, exitErr)
	return exitErr.Code, out.String()
}

// tunnelArgs names --work-session so resolving it never reads the config file.
func tunnelArgs(args ...string) []string {
	return append(args, "-l", "0", "-r", "5432", "--work-session", "550e8400-e29b-41d4-a716-446655440000")
}

func stubClient(ts *httptest.Server) func() (*client.AlpaconClient, error) {
	return func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, nil
	}
}

func TestTunnelCommand_InvalidPortIsAUsageErrorBeforeAnyClient(t *testing.T) {
	t.Parallel()
	var built atomic.Bool
	newClient := func() (*client.AlpaconClient, error) {
		built.Store(true)
		return nil, errors.New("unreachable")
	}

	code, stderr := runTunnelCommand(t, newClient, "my-server", "-l", "9000", "-r", "invalid")

	assert.Equal(t, utils.ExitCodeUsageError, code)
	assert.Contains(t, stderr, "invalid remote port")
	assert.NotContains(t, stderr, issueFooter)
	assert.False(t, built.Load(), "a usage error must not build a client")
}

func TestTunnelCommand_ClientFailureExitsOneWithTheFooter(t *testing.T) {
	t.Parallel()
	newClient := func() (*client.AlpaconClient, error) { return nil, errors.New("no config") }

	code, stderr := runTunnelCommand(t, newClient, tunnelArgs("my-server")...)

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, stderr, "Connection to Alpacon API failed: no config. Consider re-logging.\n")
	assert.Contains(t, stderr, issueFooter)
}

func TestTunnelCommand_TokenRefusalAsksForInteractiveLogin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "tunnel only", args: tunnelArgs("my-server")},
		{name: "with a local command", args: append(tunnelArgs("my-server"), "--", "psql")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var creates atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/websh/tunnels/":
					creates.Add(1)
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"code": "` + utils.APITokenScopeMissing + `"}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			t.Cleanup(ts.Close)

			code, stderr := runTunnelCommand(t, stubClient(ts), tt.args...)

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, stderr, "a token cannot open a tunnel; run 'alpacon login' without -t")
			assert.Contains(t, stderr, issueFooter)
			assert.Equal(t, int32(1), creates.Load())
		})
	}
}

func TestTunnelCommand_GateRefusalExitsThreeWithTheDiagnostic(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/websh/tunnels/":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code": "` + utils.WorkSessionRequired + `"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)

	code, stderr := runTunnelCommand(t, stubClient(ts), tunnelArgs("my-server")...)

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, code)
	assert.Contains(t, stderr, "the tunnel operation requires an active WorkSession")
	assert.Contains(t, stderr, "  target server : my-server\n")
	assert.NotContains(t, stderr, issueFooter)
}

func TestTunnelExitError(t *testing.T) {
	t.Parallel()
	gate := utils.NewExitErrorWithCode(utils.ExitCodeWorkSessionDenied, "gate")
	tests := []struct {
		name       string
		exitCode   int
		err        error
		wantCode   int
		wantStderr string
	}{
		{name: "success", exitCode: 0},
		{name: "local command failed on its own", exitCode: 7, wantCode: 7},
		{name: "gate refusal keeps its exit", exitCode: 1, err: gate, wantCode: utils.ExitCodeWorkSessionDenied, wantStderr: "gate"},
		{name: "tunnel lost under a failed command", exitCode: 5, err: errors.New("tunnel connection lost"), wantCode: 5, wantStderr: "tunnel connection lost"},
		{name: "tunnel failed before the command", exitCode: 1, err: errors.New("failed to establish"), wantCode: utils.ExitCodeGeneralError, wantStderr: "failed to establish"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tunnelExitError(tt.exitCode, tt.err)

			if tt.wantCode == 0 {
				assert.NoError(t, got)
				return
			}
			exitErr, ok := errors.AsType[*utils.ExitError](got)
			require.True(t, ok, "want an ExitError, got %v", got)
			assert.Equal(t, tt.wantCode, exitErr.Code)
			var stderr bytes.Buffer
			utils.FprintExitError(&stderr, exitErr)
			if tt.wantStderr == "" {
				assert.Empty(t, stderr.String())
				return
			}
			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}
