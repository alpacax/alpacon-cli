package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCurrent_NoActive_ReturnsEmpty(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Fail(t, "server should not be called when no active session")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	uuid, ws, err := RunCurrent(ac)
	require.NoError(t, err)
	assert.Empty(t, uuid)
	assert.Nil(t, ws)
}

func TestRunCurrent_ActiveResolves(t *testing.T) {
	setupTmpConfig(t)
	require.NoError(t, config.SetActiveWorkSession("ses-abc"))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "ses-abc",
			"description": "incident-response",
			"status":      "active",
		})
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	uuid, ws, err := RunCurrent(ac)
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", uuid)
	require.NotNil(t, ws)
	assert.Equal(t, "incident-response", ws.Description)
}

func TestRunCurrent_StaleUUID_ServerNotFound(t *testing.T) {
	setupTmpConfig(t)
	require.NoError(t, config.SetActiveWorkSession("ses-stale"))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found.", http.StatusNotFound)
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	uuid, ws, err := RunCurrent(ac)
	require.Error(t, err)
	assert.Equal(t, "ses-stale", uuid)
	assert.Nil(t, ws)
}

func currentSessionClient(t *testing.T, status string) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/work-sessions/sessions/ses-abc/" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ses-abc","status":"` + status + `"}`))
	}))
	t.Cleanup(ts.Close)
	return newTestClient(ts)
}

func TestWriteCurrentTable_MarksARunningSessionQuietly(t *testing.T) {
	ac := currentSessionClient(t, "active")

	var err error
	stdout, stderr := testutil.CaptureOutput(t, func() {
		err = WriteCurrentTable(ac, "ses-abc")
	})

	require.NoError(t, err)
	assert.Contains(t, stdout, "*")
	assert.Empty(t, stderr)
}

func TestWriteCurrentTable_WarnsAndLeavesAnEndedSessionUnmarked(t *testing.T) {
	ac := currentSessionClient(t, "completed")

	var err error
	stdout, stderr := testutil.CaptureOutput(t, func() {
		err = WriteCurrentTable(ac, "ses-abc")
	})

	require.NoError(t, err)
	assert.Contains(t, stdout, "ses-abc")
	assert.NotContains(t, stdout, "*")
	assert.Contains(t, stderr, "Active work-session ses-abc has ended (completed). Run 'alpacon work-session use --unset' to clear it.")
}

func TestWriteCurrentRaw_WarnsOnAnEndedSessionAndKeepsTheBody(t *testing.T) {
	ac := currentSessionClient(t, "completed")

	var err error
	stdout, stderr := testutil.CaptureOutput(t, func() {
		err = WriteCurrentRaw(ac, "ses-abc")
	})

	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"ses-abc","status":"completed"}`, stdout)
	assert.Contains(t, stderr, "Active work-session ses-abc has ended (completed).")
}

func TestRunCurrent_UsesClientWorkspaceWhenConfigSwitchedAfterClientBuilt(t *testing.T) {
	setupTmpConfig(t)
	require.NoError(t, config.SetActiveWorkSession("ses-a"))
	var requested string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ses-a", "status": "active"})
	}))
	defer ts.Close()
	ac := newTestClient(ts)
	switchConfigToWorkspace(t, "other", "ses-other")

	uuid, _, err := RunCurrent(ac)

	require.NoError(t, err)
	assert.Equal(t, "ses-a", uuid)
	assert.Equal(t, "/api/work-sessions/sessions/ses-a/", requested)
}

func TestPrintCurrent_UsesClientWorkspaceWhenConfigSwitchedAfterClientBuilt(t *testing.T) {
	setupTmpConfig(t)
	require.NoError(t, config.SetActiveWorkSession("ses-a"))
	var requested string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ses-a","status":"active"}`))
	}))
	defer ts.Close()
	ac := newTestClient(ts)
	switchConfigToWorkspace(t, "other", "ses-other")
	saved := utils.OutputFormat
	t.Cleanup(func() { utils.OutputFormat = saved })
	utils.OutputFormat = utils.OutputFormatJSON

	var err error
	stdout, _ := testutil.CaptureOutput(t, func() {
		err = printCurrent(ac)
	})

	require.NoError(t, err)
	assert.Equal(t, "/api/work-sessions/sessions/ses-a/", requested)
	assert.JSONEq(t, `{"id":"ses-a","status":"active"}`, stdout)
}

func TestPrintCurrent_PrintsNullWhenTheClientsWorkspaceHasNoSession(t *testing.T) {
	setupTmpConfig(t)
	ac := &client.AlpaconClient{WorkspaceName: "ws"}
	switchConfigToWorkspace(t, "other", "ses-other")
	saved := utils.OutputFormat
	t.Cleanup(func() { utils.OutputFormat = saved })
	utils.OutputFormat = utils.OutputFormatJSON

	var err error
	stdout, _ := testutil.CaptureOutput(t, func() {
		err = printCurrent(ac)
	})

	require.NoError(t, err)
	assert.Equal(t, "null\n", stdout)
}

func currentSessionServer(t *testing.T, id string, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/work-sessions/sessions/"+id+"/" {
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

func TestWorkSessionCurrentCmd_NoActiveSessionSendsNoRequest(t *testing.T) {
	tests := []struct {
		format, wantStdout, wantStderrContains string
	}{
		{format: utils.OutputFormatTable, wantStdout: "", wantStderrContains: "No active work-session."},
		{format: utils.OutputFormatJSON, wantStdout: "null\n"},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			ts := testutil.NewNoRequestServer(t)

			stdout, stderr, exitCode := runWorkSessionHelper(t, tt.format, ts.URL, "current")

			assert.Equal(t, 0, exitCode)
			assert.Equal(t, tt.wantStdout, stdout)
			if tt.wantStderrContains == "" {
				assert.Empty(t, stderr)
			} else {
				assert.Contains(t, stderr, tt.wantStderrContains)
			}
		})
	}
}

func TestWorkSessionCurrentCmd_PrintsTheActiveSession(t *testing.T) {
	body := `{"id":"ses-1","description":"incident-response","status":"active"}`
	t.Run("table", func(t *testing.T) {
		ts := currentSessionServer(t, "ses-1", http.StatusOK, body)
		home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-1"})

		stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
			utils.OutputFormatTable, ts.URL, "current")

		assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
		assert.Contains(t, stdout, "ses-1")
		assert.Contains(t, stdout, "incident-response")
	})
	t.Run("json", func(t *testing.T) {
		ts := currentSessionServer(t, "ses-1", http.StatusOK, body)
		home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-1"})

		stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
			utils.OutputFormatJSON, ts.URL, "current")

		assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
		assert.JSONEq(t, body, stdout)
	})
}

func TestWorkSessionCurrentCmd_StaleSessionFails(t *testing.T) {
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			ts := currentSessionServer(t, "ses-1", http.StatusNotFound, `{"detail":"Not found."}`)
			home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-1"})

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
				format, ts.URL, "current")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assertCommandFailure(t, format, opCurrent, stdout, stderr, utils.ExitCodeGeneralError,
				"http_404", "active work-session ses-1 no longer accessible")
			assert.Contains(t, stderr, "alpacon work-session use --unset")
		})
	}
}
