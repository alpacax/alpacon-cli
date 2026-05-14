package worksession_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCurrent_NoActive_ReturnsEmpty(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called when no active session")
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	uuid, ws, err := worksession.RunCurrent(ac)
	require.NoError(t, err)
	assert.Equal(t, "", uuid)
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

	uuid, ws, err := worksession.RunCurrent(ac)
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

	uuid, ws, err := worksession.RunCurrent(ac)
	require.Error(t, err)
	assert.Equal(t, "ses-stale", uuid)
	assert.Nil(t, ws)
}
