package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestGetAccessControl(t *testing.T) {
	expected := map[string]any{
		"allow_sudo_with_mfa":      true,
		"allow_direct_root":        false,
		"allow_tunnel_by_default":  true,
		"allow_editor_by_default":  true,
		"home_directory_permission": "750",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/workspaces/access-control/-/", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetAccessControl(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Equal(t, true, got["allow_sudo_with_mfa"])
	assert.Equal(t, false, got["allow_direct_root"])
	assert.Equal(t, true, got["allow_tunnel_by_default"])
	assert.Equal(t, true, got["allow_editor_by_default"])
	assert.Equal(t, "750", got["home_directory_permission"])
}

func TestGetAccessControl_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission denied"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := GetAccessControl(ac)
	assert.Error(t, err)
}

func TestGetAccessControl_EmptyResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetAccessControl(ac)
	assert.NoError(t, err)

	var got map[string]any
	err = json.Unmarshal(body, &got)
	assert.NoError(t, err)
	assert.Empty(t, got)
}
