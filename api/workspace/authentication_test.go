package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestGetAuthentication(t *testing.T) {
	expected := map[string]any{
		"mfa_required":         true,
		"mfa_timeout":          float64(300),
		"allowed_mfa_methods":  []any{"email", "otp"},
		"mfa_required_actions": []any{"server", "websh"},
		"passkey_as_mfa":       false,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/workspaces/security/-/", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetAuthentication(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Equal(t, true, got["mfa_required"])
	assert.Equal(t, float64(300), got["mfa_timeout"])
	assert.Equal(t, []any{"email", "otp"}, got["allowed_mfa_methods"])
	assert.Equal(t, []any{"server", "websh"}, got["mfa_required_actions"])
	assert.Equal(t, false, got["passkey_as_mfa"])
}

func TestGetAuthentication_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission denied"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := GetAuthentication(ac)
	assert.Error(t, err)
}

func TestGetAuthentication_EmptyResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetAuthentication(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Empty(t, got)
}
