package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestGetMFAMethods(t *testing.T) {
	expected := map[string]any{
		"allowed_mfa_methods": []any{"email", "otp"},
		"passkey_as_mfa":     false,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/workspaces/security/-/mfa-methods/", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetMFAMethods(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Equal(t, []any{"email", "otp"}, got["allowed_mfa_methods"])
	assert.Equal(t, false, got["passkey_as_mfa"])
}

func TestGetMFAMethods_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission denied"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := GetMFAMethods(ac)
	assert.Error(t, err)
}

func TestGetMFAMethods_EmptyResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetMFAMethods(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Empty(t, got)
}
