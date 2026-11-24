package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient pins the client to setupTmpConfig's workspace so tests write and read the same config key.
func newTestClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient:    ts.Client(),
		BaseURL:       ts.URL,
		WorkspaceName: "ws",
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

	desc, err := RunUse(ac, "ses-abc")
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

	_, err := RunUse(ac, "ses-missing")
	require.Error(t, err)

	got, _ := config.GetActiveWorkSession()
	assert.Empty(t, got, "config must not be updated on failure")
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

	_, err := RunUse(ac, "ses-pending")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pending")

	got, _ := config.GetActiveWorkSession()
	assert.Empty(t, got)
}

func TestRunUse_RejectsAgentSession(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/api/work-sessions/sessions/ses-agent/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "ses-agent",
			"description":    "agent job",
			"status":         "active",
			"requester_type": "agent",
		})
	}))
	defer ts.Close()
	ac := newTestClient(ts)

	_, err := RunUse(ac, "ses-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not workspace-attachable")

	got, _ := config.GetActiveWorkSession()
	assert.Empty(t, got, "config must not be updated for an agent session")
}

func TestRunUnset_Idempotent(t *testing.T) {
	setupTmpConfig(t)

	err := RunUnset()
	require.NoError(t, err)

	require.NoError(t, config.SetActiveWorkSession("ses-xyz"))
	err = RunUnset()
	require.NoError(t, err)
	got, _ := config.GetActiveWorkSession()
	assert.Empty(t, got)
}

// A --wait --use create can block for minutes while another shell's `alpacon ws use` rewrites the
// config's workspace; the session must file under the client's pinned workspace.
func TestRunUse_FilesUnderClientWorkspaceWhenConfigSwitchedMidCommand(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	require.NoError(t, config.CreateConfig("https://ws-a.example.com", "ws-a", "", "", "", "", "", 0, false))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "ses-a",
			"description": "deploy",
			"status":      "active",
		})
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, WorkspaceName: "ws-a"}

	before, err := config.LoadConfig()
	require.NoError(t, err)
	require.NoError(t, config.SwitchWorkspace(before, "https://ws-b.example.com", "ws-b", false, nil))

	_, err = RunUse(ac, "ses-a")
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "ses-a", cfg.ActiveWorkSessions["ws-a"])
	assert.NotContains(t, cfg.ActiveWorkSessions, "ws-b")
}

func TestRunUse_RejectsClusterSession(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/api/work-sessions/sessions/ses-kube/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "ses-kube",
			"status":         "active",
			"requester_type": "user",
			"scopes":         []string{"kube"},
			"clusters":       []map[string]string{{"id": "c-1", "name": "prod-east"}},
		})
	}))
	defer ts.Close()

	_, err := RunUse(newTestClient(ts), "ses-kube")

	require.ErrorIs(t, err, ErrClusterSessionNotAttachable)
	assert.Contains(t, err.Error(), "alpacon kube login prod-east --work-session ses-kube")
	got, _ := config.GetActiveWorkSession()
	assert.Empty(t, got, "config must not be updated for a cluster session")
}

// The server leaves a cluster the caller does not reach out of clusters and counts it, which
// must still keep the session out of the active slot.
func TestRunUse_RejectsAHiddenClusterSession(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/api/work-sessions/sessions/550e8400-e29b-41d4-a716-446655440000/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "550e8400-e29b-41d4-a716-446655440000", "status": "active", "requester_type": "user",
			"scopes": ["kube"], "servers": [], "servers_hidden": 0, "clusters": [], "clusters_hidden": 1
		}`))
	}))
	defer ts.Close()

	_, err := RunUse(newTestClient(ts), "550e8400-e29b-41d4-a716-446655440000")

	require.ErrorIs(t, err, ErrClusterSessionNotAttachable)
	assert.Contains(t, err.Error(), "targets a cluster you cannot access")
	assert.NotContains(t, err.Error(), "kube login", "a caller who cannot name the cluster has no kube login to run")
	got, _ := config.GetActiveWorkSession()
	assert.Empty(t, got, "config must not be updated for a cluster session")
}

// loggedInClient logs account A in to ts and returns the client that login builds.
func loggedInClient(t *testing.T, ts *httptest.Server) *client.AlpaconClient {
	t.Helper()
	require.NoError(t, config.CreateConfig(ts.URL, "ws", "", "", "token-a", "refresh-a", "", 3600, false))
	ac, err := client.NewAlpaconAPIClient()
	require.NoError(t, err)
	ac.WorkspaceName = "ws"
	return ac
}

func TestRunUseSessionDoesNotSaveIntoAnotherLogin(t *testing.T) {
	setupTmpConfig(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		assert.NoError(t, config.CreateConfig("https://ws.example.com", "ws", "", "", "token-b", "refresh-b", "", 3600, false)) // another terminal
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ses-a","status":"active"}`))
	}))
	defer ts.Close()

	_, err := RunUseSession(loggedInClient(t, ts), "ses-a")

	require.ErrorIs(t, err, config.ErrLoginChanged)
	stored, err := config.GetActiveWorkSessionFor("ws")
	require.NoError(t, err)
	assert.Empty(t, stored)
}
