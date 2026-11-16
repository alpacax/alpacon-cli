package exec

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecCommandWorkSessionGateExits3WithJSONDiagnostic(t *testing.T) {
	var sawCommandPost atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			sawCommandPost.Store(true)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{
				"code": "work_session_required",
				"source": "command",
				"detail": "WorkSession required"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "id")

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitCode)
	assert.Empty(t, stdout)
	assert.True(t, sawCommandPost.Load(), "expected exec command to submit the remote command request")

	var envelope struct {
		OK        bool   `json:"ok"`
		ExitCode  int    `json:"exit_code"`
		ErrorCode string `json:"error_code"`
		Reason    string `json:"reason"`
		Context   struct {
			AuthMethod    string   `json:"auth_method"`
			RequiredScope string   `json:"required_scope"`
			TargetServers []string `json:"target_servers"`
		} `json:"context"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
	assert.False(t, envelope.OK)
	assert.Equal(t, utils.ExitCodeWorkSessionDenied, envelope.ExitCode)
	assert.Equal(t, utils.WorkSessionRequired, envelope.ErrorCode)
	assert.Equal(t, "no WorkSession selected for this shell", envelope.Reason)
	assert.Equal(t, "Browser login", envelope.Context.AuthMethod)
	assert.Equal(t, "command", envelope.Context.RequiredScope)
	assert.Equal(t, []string{"prod"}, envelope.Context.TargetServers)
}

// runInProcess runs the exec command tree against workspaceURL as cmd.Execute would and returns
// what it wrote and the exit code it leaves with: args go to the exec command, or to the
// subcommand sub names, and a failure is printed to stderr with utils.FprintExitError. It sets
// HOME, the output format and the result poll tick and swaps stdout and stderr, so its callers
// stay serial. An empty outputFormat leaves the one the command line chooses.
func runInProcess(t *testing.T, workspaceURL, sub, outputFormat string, args []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	home := t.TempDir()
	writeExecCommandTestConfig(t, home, workspaceURL)
	t.Setenv("HOME", home)
	t.Setenv("ALPACON_WORK_SESSION", "")
	origFormat, origTick := utils.OutputFormat, event.CommandPollTick
	t.Cleanup(func() { utils.OutputFormat, event.CommandPollTick = origFormat, origTick })
	utils.OutputFormat = utils.OutputFormatTable
	if outputFormat != "" {
		utils.OutputFormat = outputFormat
	}
	event.CommandPollTick = 10 * time.Millisecond

	root := newCommand(testClientFactory(workspaceURL))
	var err error
	stdout, stderr = testutil.CaptureOutput(t, func() {
		if sub == "" {
			err = root.RunE(root, args) // as exec runs: Cobra's subcommand lookup is cmd.Execute's to skip
		} else {
			root.SetArgs(append([]string{sub}, args...))
			err = root.Execute()
		}
		if exitErr, ok := errors.AsType[*utils.ExitError](err); ok {
			utils.FprintExitError(os.Stderr, exitErr)
		}
	})
	if err == nil {
		return stdout, stderr, 0
	}
	exitErr, ok := errors.AsType[*utils.ExitError](err)
	require.True(t, ok, "a failure must reach cmd.Execute as utils.ExitError: %v", err)
	return stdout, stderr, exitErr.Code
}

// testClientFactory builds the client cmd.Execute would build from the config runInProcess writes.
func testClientFactory(workspaceURL string) clientFactory {
	return func() (*client.AlpaconClient, error) {
		ac := &client.AlpaconClient{HTTPClient: httpclient.New(false), BaseURL: workspaceURL, WorkspaceName: "test", UserAgent: "alpacon-test"}
		ac.SetAccessToken("access-token")
		return ac, nil
	}
}

func writeExecCommandTestConfig(t *testing.T, home, workspaceURL string) {
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
