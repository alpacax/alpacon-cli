package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const completePath = "/api/work-sessions/sessions/ses-1/complete/"

var completeFormats = []string{utils.OutputFormatTable, utils.OutputFormatJSON}

func newCompleteServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != completePath {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// completeHome returns a home whose config holds the given active sessions.
func completeHome(t *testing.T, serverURL string, active map[string]string) string {
	t.Helper()
	home := t.TempDir()
	writeWorkSessionTestConfig(t, home, serverURL)
	path := filepath.Join(home, ".alpacon", "config.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))
	cfg["active_work_sessions"] = active
	data, err = json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return home
}

func activeSessions(t *testing.T, home string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".alpacon", "config.json"))
	require.NoError(t, err)
	var cfg struct {
		Active map[string]string `json:"active_work_sessions"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	return cfg.Active
}

func parseEnvelope(t *testing.T, stderr string) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
	return env
}

func TestWorkSessionCompleteCmd_ClearsActiveSessionWhenItIsTheCompletedOne(t *testing.T) {
	t.Parallel()
	ts := newCompleteServer(t, http.StatusOK, `{}`)
	home := completeHome(t, ts.URL, map[string]string{"test": "ses-1"})

	stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
		utils.OutputFormatTable, ts.URL, "complete", "ses-1")

	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Work session ses-1 completed.")
	assert.Contains(t, stderr, "Active work-session cleared.")
	assert.NotContains(t, activeSessions(t, home), "test")
}

func TestWorkSessionCompleteCmd_KeepsOtherActiveSession(t *testing.T) {
	t.Parallel()
	ts := newCompleteServer(t, http.StatusOK, `{}`)
	home := completeHome(t, ts.URL, map[string]string{"test": "ses-other"})

	stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
		utils.OutputFormatTable, ts.URL, "complete", "ses-1")

	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Work session ses-1 completed.")
	assert.NotContains(t, stderr, "Active work-session cleared.")
	assert.Equal(t, map[string]string{"test": "ses-other"}, activeSessions(t, home))
}

func TestWorkSessionCompleteCmd_ServerRefusalFailsAndKeepsActiveSession(t *testing.T) {
	t.Parallel()
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ts := newCompleteServer(t, http.StatusForbidden,
				`{"code":"work_session_not_usable","source":"work_session","detail":"not usable"}`)
			home := completeHome(t, ts.URL, map[string]string{"test": "ses-1"})

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
				format, ts.URL, "complete", "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assert.Empty(t, stdout)
			assert.Equal(t, map[string]string{"test": "ses-1"}, activeSessions(t, home))
			if format == utils.OutputFormatJSON {
				env := parseEnvelope(t, stderr)
				assert.False(t, env.OK)
				assert.Equal(t, utils.ExitCodeGeneralError, env.ExitCode)
				assert.Equal(t, "work_session_not_usable", env.ErrorCode)
				assert.Equal(t, "complete", env.Context.Operation)
				assert.Contains(t, env.Message, "Failed to complete work session")
				return
			}
			assert.Contains(t, stderr, "Failed to complete work session")
			assert.NotContains(t, stderr, `"error_code"`)
		})
	}
}

func TestWorkSessionCompleteCmd_NotLoggedInReportsConnectionFailure(t *testing.T) {
	t.Parallel()
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + t.TempDir()},
				format, ts.URL, "complete", "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assert.Empty(t, stdout)
			if format == utils.OutputFormatJSON {
				env := parseEnvelope(t, stderr)
				assert.False(t, env.OK)
				assert.Equal(t, utils.ExitCodeGeneralError, env.ExitCode)
				assert.Equal(t, utils.ErrorCodeNotLoggedIn, env.ErrorCode)
				assert.Equal(t, "complete", env.Context.Operation)
				assert.Contains(t, env.Message, "Connection to Alpacon API failed")
				return
			}
			assert.Contains(t, stderr, "Connection to Alpacon API failed")
		})
	}
}

func TestWorkSessionCompleteCmd_InvalidSessionIDIsRefusedBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, nil, format, ts.URL, "complete", "../x")

			assert.Equal(t, utils.ExitCodeUsageError, exitCode)
			assert.Empty(t, stdout)
			if format == utils.OutputFormatJSON {
				env := parseEnvelope(t, stderr)
				assert.False(t, env.OK)
				assert.Equal(t, utils.ExitCodeUsageError, env.ExitCode)
				assert.Equal(t, utils.ErrorCodeUsage, env.ErrorCode)
				assert.Equal(t, "complete", env.Context.Operation)
				return
			}
			assert.Contains(t, stderr, "Failed to complete work session")
		})
	}
}
