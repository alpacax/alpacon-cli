package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPreferences(t *testing.T) {
	t.Parallel()
	expected := map[string]any{
		"language":              "ko",
		"timezone":              "Asia/Seoul",
		"invite_ttl":            172800,
		"websh_session_timeout": 3600,
		"auto_agent_upgrade":    true,
		"package_proxy":         nil,
		"front_url":             "https://example.alpacon.io",
		"country":               "KR",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/workspaces/preferences/-/", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetPreferences(ac)
	require.NoError(t, err)

	expectedJSON, err := json.Marshal(expected)
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedJSON), string(body))
}

func TestGetPreferences_ServerError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission denied"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := GetPreferences(ac)
	assert.Error(t, err)
}

func TestGetPreferences_EmptyResponse(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetPreferences(ac)
	require.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	require.NoError(t, err)
	assert.Empty(t, got)
}
