package worksession_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}
}

func setupTmpConfig(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	require.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "", "", "", 0, false))
}

func TestRunUse_Success_PersistsToConfig(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/work-sessions/sessions/ses-abc/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "ses-abc",
			"description": "incident-response",
			"status":      "active",
		})
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	desc, err := worksession.RunUse(ac, "ses-abc")
	require.NoError(t, err)
	assert.Equal(t, "incident-response", desc)

	got, err := config.GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", got)
}

func TestRunUse_NotFound(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found.", http.StatusNotFound)
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	_, err := worksession.RunUse(ac, "ses-missing")
	require.Error(t, err)

	got, _ := config.GetActiveWorkSession()
	assert.Equal(t, "", got, "config must not be updated on failure")
}

func TestRunUse_RejectsNonActiveStatus(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "ses-pending",
			"description": "queue",
			"status":      "pending",
		})
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	_, err := worksession.RunUse(ac, "ses-pending")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pending")

	got, _ := config.GetActiveWorkSession()
	assert.Equal(t, "", got)
}

func TestRunUnset_Idempotent(t *testing.T) {
	setupTmpConfig(t)

	// First call on empty
	err := worksession.RunUnset()
	require.NoError(t, err)

	// Set something, unset, verify clear
	require.NoError(t, config.SetActiveWorkSession("ses-xyz"))
	err = worksession.RunUnset()
	require.NoError(t, err)
	got, _ := config.GetActiveWorkSession()
	assert.Equal(t, "", got)
}
