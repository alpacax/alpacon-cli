package auth0

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The signer's API grants no refresh token, so the second authorization must
// not ask for one: it carries the login's org scope and nothing that outlives
// the access token.
func TestSignerDeviceCodeScope(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "openid profile email cli org:myws device:"+testDeviceID, signerDeviceCodeScope("myws", testDeviceID))
	assert.Equal(t, "openid profile email cli org:myws", signerDeviceCodeScope("myws", ""))
	assert.NotContains(t, signerDeviceCodeScope("myws", testDeviceID), "offline_access")
}

func TestRequestSignerDeviceCode_AsksForTheSignerAudience(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var sent map[string]string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSuffix(r.URL.Path, "/") != deviceCodePath {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Errorf("decoding the device-code request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"device_code":"dc","user_code":"ABCD-EFGH","verification_uri_complete":"https://auth.example.com/activate?user_code=ABCD-EFGH","expires_in":900,"interval":5}`)
	}))
	defer ts.Close()

	envInfo := &AuthEnvResponse{Auth0: Auth0Config{
		Method:     "auth0",
		ClientID:   "cli-client",
		Domain:     strings.TrimPrefix(ts.URL, "https://"),
		Audience:   "https://alpacon.io/",
		SchemaName: "frozen-name",
	}}

	deviceCode, err := RequestSignerDeviceCode("myws", ts.Client(), envInfo, "https://alpacon.ai/")

	require.NoError(t, err)
	assert.Equal(t, "ABCD-EFGH", deviceCode.UserCode)
	assert.Equal(t, "https://alpacon.ai/", sent["audience"], "the workspace audience must not be requested here")
	assert.Equal(t, "cli-client", sent["client_id"])
	assert.Contains(t, sent["scope"], "org:frozen-name")
	assert.NotContains(t, sent["scope"], "offline_access")
}

func TestFetchAuthEnv_CommandACLSurface(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want CommandACLSurface
	}{
		{name: "ledger", body: `{"auth0": {"method": "auth0", "workspace_id": "8b0f2c8e-1d4a-4c3b-9a55-0c1d2e3f4a5b"}, "command_acl_surface": "ai"}`, want: CommandACLSurfaceAI},
		{name: "workspace keeps its ACLs", body: `{"auth0": {"method": "local"}, "command_acl_surface": "upstream"}`, want: "upstream"},
		{name: "older server", body: `{"auth0": {"method": "auth0"}}`, want: ""},
		{name: "malformed value", body: `{"auth0": {"method": "auth0"}, "command_acl_surface": {"ai": true}}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer ts.Close()

			envInfo, err := FetchAuthEnv(ts.URL, ts.Client())

			require.NoError(t, err, "a malformed surface must not break the login that reads the same response")
			assert.Equal(t, tt.want, envInfo.CommandACLSurface)
		})
	}
}

func TestFetchAuthEnv_WorkspaceID(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"auth0": {"method": "auth0", "workspace_id": "8b0f2c8e-1d4a-4c3b-9a55-0c1d2e3f4a5b"}}`)
	}))
	defer ts.Close()

	envInfo, err := FetchAuthEnv(ts.URL, ts.Client())

	require.NoError(t, err)
	assert.Equal(t, "8b0f2c8e-1d4a-4c3b-9a55-0c1d2e3f4a5b", envInfo.Auth0.WorkspaceID)
}

func TestMapAuth0Error_ReturnsSentinels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code string
		want error
	}{
		{name: "declined in the browser", code: "access_denied", want: ErrDeviceCodeDenied},
		{name: "device code expired", code: "expired_token", want: ErrDeviceCodeExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mapAuth0Error(&oauthError{Code: tt.code, Desc: "refused"})
			assert.ErrorIs(t, err, tt.want)
		})
	}

	other := &oauthError{Code: "invalid_grant", Desc: "refused"}
	assert.NotErrorIs(t, mapAuth0Error(other), ErrDeviceCodeDenied)
}

// The ledger words its own remedy from these sentinels, so PollForToken has to
// return them unwrapped enough for errors.Is.
func TestPollForToken_ReturnsSentinels(t *testing.T) {
	tests := []struct {
		name string
		code string
		want error
	}{
		{name: "declined", code: "access_denied", want: ErrDeviceCodeDenied},
		{name: "still pending when the code runs out", code: "authorization_pending", want: ErrDeviceCodeTimedOut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := requestAccessTokenFunc
			t.Cleanup(func() { requestAccessTokenFunc = orig })
			requestAccessTokenFunc = func(string, *AuthEnvResponse) (*TokenResponse, error) {
				return nil, &oauthError{Code: tt.code, Desc: "refused"}
			}

			_, err := PollForToken(&DeviceCodeResponse{DeviceCode: "dc", ExpiresIn: 1, Interval: 5}, &AuthEnvResponse{})

			assert.ErrorIs(t, err, tt.want)
		})
	}
}

// The extraction that added the signer variant must leave the login's request
// as it was: the workspace audience, and offline_access for its refresh token.
func TestRequestDeviceCode_KeepsTheWorkspaceAudience(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var sent map[string]string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Errorf("decoding the device-code request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"device_code":"dc","user_code":"ABCD-EFGH","expires_in":900,"interval":5}`)
	}))
	defer ts.Close()
	envInfo := &AuthEnvResponse{Auth0: Auth0Config{
		Method: "auth0", ClientID: "cli-client", Domain: strings.TrimPrefix(ts.URL, "https://"),
		Audience: "https://alpacon.io/", SchemaName: "frozen-name",
	}}

	_, err := RequestDeviceCode("myws", ts.Client(), envInfo)

	require.NoError(t, err)
	assert.Equal(t, "https://alpacon.io/", sent["audience"])
	assert.Equal(t, "cli-client", sent["client_id"])
	assert.Contains(t, sent["scope"], "offline_access")
	assert.Contains(t, sent["scope"], "org:frozen-name")
}
