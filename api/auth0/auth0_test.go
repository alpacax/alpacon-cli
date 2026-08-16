package auth0

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDeviceID = "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b"

func TestAppendDeviceScope(t *testing.T) {
	const base = "openid profile email offline_access cli org:myws"

	tests := []struct {
		name     string
		deviceID string
		expected string
	}{
		{
			name:     "Well-formed identifier is appended last",
			deviceID: testDeviceID,
			expected: base + " device:" + testDeviceID,
		},
		{
			name:     "Missing identifier leaves the scope untouched",
			deviceID: "",
			expected: base,
		},
		{
			name:     "Too short for the Auth0 action pattern is dropped",
			deviceID: "abc1234",
			expected: base,
		},
		{
			name:     "Too long for the Auth0 action pattern is dropped",
			deviceID: strings.Repeat("a", 65),
			expected: base,
		},
		{
			name:     "Illegal character is dropped",
			deviceID: "not_a_valid_id",
			expected: base,
		},
		{
			name:     "Embedded space cannot inject a second scope",
			deviceID: "abcd1234 admin",
			expected: base,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, appendDeviceScope(base, tt.deviceID))
		})
	}
}

func TestDeviceCodeScope(t *testing.T) {
	assert.Equal(t,
		"openid profile email offline_access cli org:myws device:"+testDeviceID,
		deviceCodeScope("myws", testDeviceID),
	)
	assert.Equal(t,
		"openid profile email offline_access cli org:myws",
		deviceCodeScope("myws", ""),
	)
}

func TestRefreshScope(t *testing.T) {
	assert.Equal(t, "cli org:myws device:"+testDeviceID, refreshScope("myws", testDeviceID))
	assert.Equal(t, "cli org:myws", refreshScope("myws", ""))
}

// TestCurrentDeviceID_StableAcrossCalls pins that both scope builders see the
// same identifier on every invocation of the CLI.
func TestCurrentDeviceID_StableAcrossCalls(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first := currentDeviceID()
	require.True(t, config.IsValidDeviceID(first), "device id must satisfy the Auth0 action pattern: %q", first)
	assert.Equal(t, first, currentDeviceID())
}

func TestResolveOrgName(t *testing.T) {
	tests := []struct {
		name     string
		envInfo  *AuthEnvResponse
		fallback string
		expected string
	}{
		{
			name: "Server-provided schema name wins over the fallback label",
			envInfo: &AuthEnvResponse{
				Auth0: Auth0Config{Method: "auth0", SchemaName: "frozen-name"},
			},
			fallback: "renamed-label",
			expected: "frozen-name",
		},
		{
			name: "Older server without schema name falls back to the label",
			envInfo: &AuthEnvResponse{
				Auth0: Auth0Config{Method: "auth0"},
			},
			fallback: "myworkspace",
			expected: "myworkspace",
		},
		{
			name:     "Nil env info falls back to the label",
			envInfo:  nil,
			fallback: "myworkspace",
			expected: "myworkspace",
		},
		{
			name: "Empty fallback stays empty when the server omits the field",
			envInfo: &AuthEnvResponse{
				Auth0: Auth0Config{Method: "auth0"},
			},
			fallback: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, resolveOrgName(tt.envInfo, tt.fallback))
		})
	}
}

func TestFetchAuthEnv(t *testing.T) {
	tests := []struct {
		name             string
		responseBody     string
		expectSchemaName string
	}{
		{
			name: "Server provides the frozen schema name",
			responseBody: `{
				"auth0": {
					"method": "auth0",
					"client_id": "client123",
					"domain": "auth.example.com",
					"audience": "https://api.example.com/",
					"schema_name": "frozen-name",
					"workspace_id": "8b0f2c8e-0000-0000-0000-000000000000"
				},
				"language": "en"
			}`,
			expectSchemaName: "frozen-name",
		},
		{
			name: "Older server omits the schema name",
			responseBody: `{
				"auth0": {
					"method": "auth0",
					"client_id": "client123",
					"domain": "auth.example.com",
					"audience": "https://api.example.com/"
				},
				"language": "en"
			}`,
			expectSchemaName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/auth/env/", r.URL.Path)
				assert.Equal(t, "cli", r.URL.Query().Get("client"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer ts.Close()

			envInfo, err := FetchAuthEnv(ts.URL, ts.Client())
			assert.NoError(t, err)
			assert.Equal(t, "auth0", envInfo.Auth0.Method)
			assert.Equal(t, "client123", envInfo.Auth0.ClientID)
			assert.Equal(t, tt.expectSchemaName, envInfo.Auth0.SchemaName)
		})
	}
}

func TestExtractSubdomain(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		expected  string
		expectErr bool
	}{
		{
			name:     "Cloud workspace URL",
			url:      "https://myws.us1.alpacon.io",
			expected: "myws",
		},
		{
			name:      "Host without a subdomain",
			url:       "https://example.io",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subdomain, err := extractSubdomain(tt.url)
			if tt.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, subdomain)
		})
	}
}
