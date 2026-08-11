package worksession

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
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helperSetupExitCode marks a helper that failed before reaching the command, so a
// broken harness is never read as the command's own exit 2.
const helperSetupExitCode = 99

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
	// The subprocess has no TTY, so IsInteractiveShell() is false.
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
	// Table is the default output mode, so this is the branch of
	// CliUsageErrorEnvelopeWithExit most callers reach.
	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, "http://127.0.0.1:1",
		"extend", "ses-1")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Empty(t, stdout)
	assert.NotContains(t, stderr, `"error_code"`)
	assert.Contains(t, stderr, "--expires-in or --expires-at")
}

func TestUseJSONErrorEnvelope_LocalStateError(t *testing.T) {
	// GET succeeds but the session is expired — local validation error with err,
	// no server code: error_code must be omitted.
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
	assert.Empty(t, env.ErrorCode)
	assert.Equal(t, "use", env.Context.Operation)
	assert.Contains(t, env.Message, "cannot be used")
	assert.NotContains(t, stderr, "error_code")
}

// runWorkSessionHelper re-runs the test binary as a subprocess executing the given
// work-session subcommand in the given output mode; returns (stdout, stderr, exitCode).
func runWorkSessionHelper(t *testing.T, outputFormat, serverURL string, args ...string) (string, string, int) {
	t.Helper()
	// outputFormat and serverURL are adjacent strings: a transposed call site
	// still compiles.
	require.Contains(t, []string{utils.OutputFormatTable, utils.OutputFormatJSON}, outputFormat)

	home := t.TempDir()
	writeWorkSessionTestConfig(t, home, serverURL)

	helperArgs := append(
		[]string{"-test.run=^TestWorkSessionErrorHelperProcess$", "--", "worksession-error-helper"},
		args...,
	)
	helper := osexec.Command(os.Args[0], helperArgs...)
	helper.Env = append(os.Environ(),
		"GO_WANT_WORKSESSION_ERROR_HELPER=1",
		"GO_WORKSESSION_HELPER_OUTPUT="+outputFormat,
		"HOME="+home,
	)
	var stdout, stderr bytes.Buffer
	helper.Stdout = &stdout
	helper.Stderr = &stderr

	err := helper.Run()
	exitCode := 0
	if err != nil {
		var exitErr *osexec.ExitError
		require.True(t, errors.As(err, &exitErr), "expected exit error, got %T: %v", err, err)
		exitCode = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), exitCode
}

func TestWorkSessionErrorHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WORKSESSION_ERROR_HELPER") != "1" {
		return
	}
	args, ok := workSessionHelperArgs(os.Args)
	if !ok {
		fmt.Fprintln(os.Stderr, "missing worksession-error-helper marker")
		os.Exit(helperSetupExitCode)
	}
	outputFormat := os.Getenv("GO_WORKSESSION_HELPER_OUTPUT")
	if outputFormat != utils.OutputFormatTable && outputFormat != utils.OutputFormatJSON {
		fmt.Fprintf(os.Stderr, "unset or unknown GO_WORKSESSION_HELPER_OUTPUT: %q\n", outputFormat)
		os.Exit(helperSetupExitCode)
	}
	utils.OutputFormat = outputFormat
	WorkSessionCmd.SetArgs(args)
	if err := WorkSessionCmd.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func workSessionHelperArgs(args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == "worksession-error-helper" {
			return args[i+1:], true
		}
	}
	return nil, false
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
