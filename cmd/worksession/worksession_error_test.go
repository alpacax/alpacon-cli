package worksession

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errorEnvelope struct {
	OK        bool   `json:"ok"`
	ExitCode  int    `json:"exit_code"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Context   struct {
		Operation string `json:"operation"`
	} `json:"context"`
}

func TestExtendJSONErrorEnvelope_ServerCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/ses-1/extend/" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"work_session_not_usable","source":"work_session","detail":"not usable"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL,
		"extend", "ses-1", "--expires-in", "1h")

	assert.Equal(t, 1, exitCode)
	assert.Empty(t, stdout)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
	assert.False(t, env.OK)
	assert.Equal(t, 1, env.ExitCode)
	assert.Equal(t, "work_session_not_usable", env.ErrorCode)
	assert.Equal(t, "extend", env.Context.Operation)
	assert.Contains(t, env.Message, "Failed to extend work session")
}

func TestExtendJSONErrorEnvelope_UsageError(t *testing.T) {
	// Flag validation fails before any HTTP call; no live server needed.
	// The runner's stdin is a pipe, so IsInteractiveShell() is false.
	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, "http://127.0.0.1:1",
		"extend", "ses-1")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Empty(t, stdout)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
	assert.Equal(t, utils.ExitCodeUsageError, env.ExitCode)
	assert.Equal(t, "usage_error", env.ErrorCode)
	assert.Equal(t, "extend", env.Context.Operation)
	assert.Contains(t, env.Message, "--expires-in or --expires-at")
}

func TestExtendTableUsageError_ExitsTwo(t *testing.T) {
	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, "http://127.0.0.1:1",
		"extend", "ses-1")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Empty(t, stdout)
	assert.NotContains(t, stderr, `"error_code"`)
	assert.Contains(t, stderr, "--expires-in or --expires-at")
}

func TestUseJSONErrorEnvelope_LocalStateError(t *testing.T) {
	// GET succeeds but the session is expired: a local refusal with no server code.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-1/" {
			_, _ = w.Write([]byte(`{"id":"ses-1","status":"expired","description":"old"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL, "use", "ses-1")

	assert.Equal(t, 1, exitCode)
	assert.Empty(t, stdout)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
	assert.False(t, env.OK)
	assert.Equal(t, utils.ErrorCodeCLI, env.ErrorCode)
	assert.Equal(t, "use", env.Context.Operation)
	assert.Contains(t, env.Message, "cannot be used")
}

// runWorkSessionHelper runs the given work-session subcommand in process, as cmd.Execute would, in
// the given output mode; returns (stdout, stderr, exitCode).
func runWorkSessionHelper(t *testing.T, outputFormat, serverURL string, args ...string) (string, string, int) {
	t.Helper()
	return runWorkSessionHelperWithEnv(t, nil, outputFormat, serverURL, args...)
}

// runWorkSessionHelperWithEnv is runWorkSessionHelper with env ("KEY=value") set last, overriding HOME.
// It sets process-wide state (env, utils.OutputFormat, stdin, stdout and stderr), so its callers stay serial.
func runWorkSessionHelperWithEnv(t *testing.T, env []string, outputFormat, serverURL string, args ...string) (string, string, int) {
	t.Helper()
	return runWorkSessionTree(t, NewWorkSessionCmd(client.NewAlpaconAPIClient, utils.Stdio), env, outputFormat, serverURL, args...)
}

// runWorkSessionTree is runWorkSessionHelperWithEnv on a tree the caller built.
func runWorkSessionTree(t *testing.T, root *cobra.Command, env []string, outputFormat, serverURL string, args ...string) (string, string, int) {
	t.Helper()
	// outputFormat and serverURL are adjacent strings: a transposed call site
	// still compiles.
	require.Contains(t, []string{utils.OutputFormatTable, utils.OutputFormatJSON}, outputFormat)

	home := t.TempDir()
	writeWorkSessionTestConfig(t, home, serverURL)
	t.Setenv("HOME", home)
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	origFormat, origStdin := utils.OutputFormat, os.Stdin
	stdin, stdinWriter, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, stdinWriter.Close()) // a prompt reads EOF, as in a run with no input
	t.Cleanup(func() {
		utils.OutputFormat, os.Stdin = origFormat, origStdin
		_ = stdin.Close()
	})
	utils.OutputFormat, os.Stdin = outputFormat, stdin

	root.SetArgs(args)
	utils.MarkHookErrors(root)
	var runErr error
	stdout, stderr := testutil.CaptureOutput(t, func() {
		runErr = root.Execute()
		if exitErr, ok := errors.AsType[*utils.ExitError](runErr); ok {
			utils.FprintExitError(os.Stderr, exitErr)
		}
	})
	if runErr == nil {
		return stdout, stderr, 0
	}
	if exitErr, ok := errors.AsType[*utils.ExitError](runErr); ok {
		return stdout, stderr, exitErr.Code
	}
	if utils.IsHookError(runErr) && !utils.IsUsageError(runErr) {
		return stdout, stderr, utils.ExitCodeGeneralError // cmd.Execute exits 1 on a hook's own unmarked error
	}
	return stdout, stderr, utils.ExitCodeUsageError // a Cobra error or a usage-marked hook error
}

func writeWorkSessionTestConfig(t *testing.T, home, workspaceURL string) {
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

func TestRunWorkSessionHelper_ExitsLikeExecuteForAnErrorRunEReturns(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "plain error exits 1", err: errors.New("boom"), wantCode: utils.ExitCodeGeneralError},
		{name: "usage-marked error exits 2", err: utils.MarkUsageError(errors.New("bad flag")), wantCode: utils.ExitCodeUsageError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return tc.err }}
			root := NewWorkSessionCmd(client.NewAlpaconAPIClient, utils.Stdio)
			root.AddCommand(probe)

			_, _, exitCode := runWorkSessionTree(t, root, nil, utils.OutputFormatTable, "http://127.0.0.1:1", "probe")

			assert.Equal(t, tc.wantCode, exitCode)
		})
	}
}
