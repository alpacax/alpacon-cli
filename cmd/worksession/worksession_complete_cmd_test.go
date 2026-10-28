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

var completeFormats = []string{utils.OutputFormatTable, utils.OutputFormatJSON}

func newCompleteServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/work-sessions/sessions/ses-1/complete/" {
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

func completeHomeWithActiveSessions(t *testing.T, serverURL string, active map[string]string) string {
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

func completeActiveSessions(t *testing.T, home string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".alpacon", "config.json"))
	require.NoError(t, err)
	var cfg struct {
		Active map[string]string `json:"active_work_sessions"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	return cfg.Active
}

func assertCompleteFailure(t *testing.T, format, stdout, stderr string, exitCode int, errorCode, message string) {
	t.Helper()
	assert.Empty(t, stdout)
	if format == utils.OutputFormatJSON {
		var env errorEnvelope
		require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
		assert.False(t, env.OK)
		assert.Equal(t, exitCode, env.ExitCode)
		assert.Equal(t, errorCode, env.ErrorCode)
		assert.Equal(t, opComplete, env.Context.Operation)
		assert.Contains(t, env.Message, message)
		return
	}
	assert.Contains(t, stderr, message)
	assert.NotContains(t, stderr, `"error_code"`)
}

func TestWorkSessionCompleteCmd_ClearsActiveSessionWhenItIsTheCompletedOne(t *testing.T) {
	t.Parallel()
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ts := newCompleteServer(t, http.StatusOK, `{}`)
			home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-1", "other-ws": "ses-1"})

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
				format, ts.URL, "complete", "ses-1")

			assert.Equal(t, 0, exitCode)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Work session ses-1 completed.")
			assert.Contains(t, stderr, "Active work-session cleared.")
			assert.Equal(t, map[string]string{"other-ws": "ses-1"}, completeActiveSessions(t, home))
		})
	}
}

func TestWorkSessionCompleteCmd_KeepsOtherActiveSession(t *testing.T) {
	t.Parallel()
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ts := newCompleteServer(t, http.StatusOK, `{}`)
			home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-other"})

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
				format, ts.URL, "complete", "ses-1")

			assert.Equal(t, 0, exitCode)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Work session ses-1 completed.")
			assert.NotContains(t, stderr, "Active work-session cleared.")
			assert.Equal(t, map[string]string{"test": "ses-other"}, completeActiveSessions(t, home))
		})
	}
}

func TestWorkSessionCompleteCmd_ServerRefusalFailsAndKeepsActiveSession(t *testing.T) {
	t.Parallel()
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ts := newCompleteServer(t, http.StatusForbidden,
				`{"code":"work_session_not_usable","source":"work_session","detail":"not usable"}`)
			home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-1"})

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
				format, ts.URL, "complete", "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assertCompleteFailure(t, format, stdout, stderr, utils.ExitCodeGeneralError,
				"work_session_not_usable", "Failed to complete work session")
			assert.Contains(t, stderr, "not usable")
			assert.Equal(t, map[string]string{"test": "ses-1"}, completeActiveSessions(t, home))
		})
	}
}

func TestWorkSessionCompleteCmd_NotLoggedInReportsConnectionFailure(t *testing.T) {
	t.Parallel()
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			homeWithoutConfig := t.TempDir()

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + homeWithoutConfig},
				format, "http://127.0.0.1:1", "complete", "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assertCompleteFailure(t, format, stdout, stderr, utils.ExitCodeGeneralError,
				utils.ErrorCodeNotLoggedIn, "Connection to Alpacon API failed")
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
			assertCompleteFailure(t, format, stdout, stderr, utils.ExitCodeUsageError,
				utils.ErrorCodeUsage, "must be a single path segment")
		})
	}
}
