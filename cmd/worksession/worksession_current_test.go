package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
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
