package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type endSessionFunc func(ac *client.AlpaconClient, id string) (bool, error)

var endSessionCommands = []struct {
	name   string
	action string
	run    endSessionFunc
}{
	{"complete", "complete", RunComplete},
	{"revoke", "revoke", RunRevoke},
}

func endSessionServer(t *testing.T, id, action string, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/work-sessions/sessions/"+id+"/"+action+"/" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestEndingTheActiveSessionClearsIt(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			require.NoError(t, config.SetActiveWorkSession("ses-abc"))
			ac := newTestClient(endSessionServer(t, "ses-abc", tc.action, http.StatusOK, `{}`))

			cleared, err := tc.run(ac, "ses-abc")

			require.NoError(t, err)
			assert.True(t, cleared)
			got, err := config.GetActiveWorkSession()
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestEndingAnotherSessionKeepsTheActiveOne(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			require.NoError(t, config.SetActiveWorkSession("ses-keep"))
			ac := newTestClient(endSessionServer(t, "ses-other", tc.action, http.StatusOK, `{}`))

			cleared, err := tc.run(ac, "ses-other")

			require.NoError(t, err)
			assert.False(t, cleared)
			got, err := config.GetActiveWorkSession()
			require.NoError(t, err)
			assert.Equal(t, "ses-keep", got)
		})
	}
}

func TestFailingToEndTheActiveSessionKeepsIt(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			require.NoError(t, config.SetActiveWorkSession("ses-abc"))
			ac := newTestClient(endSessionServer(t, "ses-abc", tc.action, http.StatusForbidden, `{}`))

			cleared, err := tc.run(ac, "ses-abc")

			require.Error(t, err)
			assert.False(t, cleared)
			got, err := config.GetActiveWorkSession()
			require.NoError(t, err)
			assert.Equal(t, "ses-abc", got)
		})
	}
}

func TestEndingTheActiveSessionWarnsWhenTheConfigCannotBeCleared(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			home, err := os.UserHomeDir()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(home, config.ConfigFileDir, config.ConfigFileName), []byte("{"), 0600))
			ac := newTestClient(endSessionServer(t, "ses-abc", tc.action, http.StatusOK, `{}`))

			var cleared bool
			_, stderr := testutil.CaptureOutput(t, func() {
				cleared, err = tc.run(ac, "ses-abc")
			})

			require.NoError(t, err)
			assert.False(t, cleared)
			assert.Contains(t, stderr, "Work session ses-abc has ended but is still the active work-session")
			assert.Contains(t, stderr, "Run 'alpacon work-session use --unset' to clear it.")
		})
	}
}

var completeFormats = []string{utils.OutputFormatTable, utils.OutputFormatJSON}

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
			ts := endSessionServer(t, "ses-1", opComplete, http.StatusOK, `{}`)
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
			ts := endSessionServer(t, "ses-1", opComplete, http.StatusOK, `{}`)
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
			ts := endSessionServer(t, "ses-1", opComplete, http.StatusForbidden,
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
