package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestGetNotifications(t *testing.T) {
	expected := map[string]any{
		"disconnection_notification": true,
		"notification_channels":      []any{"email", "webhook"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/workspaces/notifications/", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetNotifications(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Equal(t, true, got["disconnection_notification"])
	assert.Equal(t, []any{"email", "webhook"}, got["notification_channels"])
}

func TestGetNotifications_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission denied"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := GetNotifications(ac)
	assert.Error(t, err)
}

func TestGetNotifications_EmptyResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetNotifications(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Empty(t, got)
}
