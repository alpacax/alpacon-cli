package auth0

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDeviceID = "0f6f3f2e-2a9d-4a1e-8f2b-1c2d3e4f5a6b"

	// refreshSuccessBody and fallbackScope describe the refresh exchange the
	// scope-negotiation tests below drive.
	refreshSuccessBody = `{"access_token":"new-access-token","expires_in":3600,"token_type":"Bearer"}`
	fallbackScope      = "cli org:myws"
)

// tokenRequest is the part of a token-endpoint exchange those tests care about.
type tokenRequest struct {
	GrantType string `json:"grant_type"`
	Scope     string `json:"scope"`
}

// refreshServer stands in for Auth0: it serves the workspace's auth env and the
// token endpoint, records every exchange, and answers each one however the test
// tells it to.
type refreshServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []tokenRequest
	respond  func(tokenRequest) (int, string)
}

func TestAppendDeviceScope(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// --- Refresh-token scope negotiation ---------------------------------------

func newRefreshServer(t *testing.T, respond func(tokenRequest) (int, string)) *refreshServer {
	t.Helper()
	server := &refreshServer{respond: respond}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/env/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"auth0":{"method":"auth0","client_id":"client123","domain":"`+
			strings.TrimPrefix(server.URL, "https://")+
			`","audience":"https://api.example.com/","schema_name":"myws"},"language":"en"}`)
	})
	mux.HandleFunc("/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var request tokenRequest
		if err = json.Unmarshal(body, &request); !assert.NoError(t, err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		server.mu.Lock()
		server.requests = append(server.requests, request)
		server.mu.Unlock()

		status, payload := server.respond(request)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	})

	server.Server = httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

// exchanges returns the token exchanges the CLI made, in order.
func (s *refreshServer) exchanges() []tokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tokenRequest(nil), s.requests...)
}

func oauthErrorBody(code, description string) string {
	return `{"error":"` + code + `","error_description":"` + description + `"}`
}

// rejectDeviceScopeWith answers the way an authorization server applying RFC
// 6749 §6 would to a refresh asking for a scope outside the original grant,
// which is what every installation that logged in before the device scope
// shipped holds, refusing it under the given error code.
func rejectDeviceScopeWith(code string) func(tokenRequest) (int, string) {
	return func(request tokenRequest) (int, string) {
		if strings.Contains(request.Scope, "device:") {
			return http.StatusForbidden, oauthErrorBody(code, "the requested scope exceeds the original grant")
		}
		return http.StatusOK, refreshSuccessBody
	}
}

// setupRefreshConfig points the CLI at server with a logged-in config, so
// RefreshAccessToken has a refresh token to exchange and somewhere to store the
// access token it gets back.
func setupRefreshConfig(t *testing.T, server *refreshServer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(
		server.URL, "myws",
		"", "", "old-access-token", "refresh-token",
		"alpacon.io", 3600, false,
	))
}

// captureStderr returns everything fn writes to stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original }()

	captured := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		captured <- string(data)
	}()

	fn()
	require.NoError(t, writer.Close())
	return <-captured
}

// TestRefreshAccessToken_RetriesWithoutTheDeviceScopeWhenRejected is the whole
// point of the fallback. Installations that logged in before the device scope
// shipped hold a refresh token granted without it, so an authorization server
// following RFC 6749 §6 may refuse the exchange. Without the retry every one of
// them would be logged out at once the next time its access token expired.
func TestRefreshAccessToken_RetriesWithoutTheDeviceScopeWhenRejected(t *testing.T) {
	server := newRefreshServer(t, rejectDeviceScopeWith("invalid_scope"))
	setupRefreshConfig(t, server)

	tokenRes, err := RefreshAccessToken(server.URL, server.Client(), "refresh-token")
	require.NoError(t, err)
	assert.Equal(t, "new-access-token", tokenRes.AccessToken)

	exchanges := server.exchanges()
	require.Len(t, exchanges, 2, "the device scope is attempted first and retried without it")
	assert.Equal(t, "refresh_token", exchanges[0].GrantType)
	assert.Contains(t, exchanges[0].Scope, "device:")
	assert.Equal(t, fallbackScope, exchanges[1].Scope, "the retry drops only the device scope")

	// The refreshed token reached the config, so the next command is authenticated.
	storedConfig, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "new-access-token", storedConfig.AccessToken)
}

// TestRefreshAccessToken_RetriesEveryScopeShapedRefusal walks the codes an
// authorization server can use to say it will not grant the requested scope.
// invalid_scope is RFC 6749 §5.2's own name for it; invalid_request is where a
// server that files an unrecognized scope token as a bad parameter lands
// instead. Both have to reach the retry, because a scope refusal that is missed
// degrades presence keying for the whole life of the login.
func TestRefreshAccessToken_RetriesEveryScopeShapedRefusal(t *testing.T) {
	for _, code := range []string{"invalid_scope", "invalid_request"} {
		t.Run(code, func(t *testing.T) {
			server := newRefreshServer(t, rejectDeviceScopeWith(code))
			setupRefreshConfig(t, server)

			tokenRes, err := RefreshAccessToken(server.URL, server.Client(), "refresh-token")
			require.NoError(t, err)
			assert.Equal(t, "new-access-token", tokenRes.AccessToken)

			exchanges := server.exchanges()
			require.Len(t, exchanges, 2, "the device scope is attempted first and retried without it")
			assert.Equal(t, fallbackScope, exchanges[1].Scope, "the retry drops only the device scope")
		})
	}
}

// TestRefreshAccessToken_DoesNotRetryARefusalTheScopeCannotFix is why the retry
// is an allowlist of scope-shaped codes rather than "anything the server sent
// back". A rate limit is the case that forced it: retrying there doubles the
// request rate against an endpoint already throttling the CLI, and the second
// request cannot succeed because the scope was never what it objected to.
//
// Auth0 reports that condition under more than one code—`too_many_requests`,
// and `access_denied` carrying "Global limit has been reached"—which is exactly
// why the decision cannot be a list of codes to skip.
func TestRefreshAccessToken_DoesNotRetryARefusalTheScopeCannotFix(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		desc   string
	}{
		{
			name:   "Rate limit",
			status: http.StatusTooManyRequests,
			code:   "too_many_requests",
			desc:   "global limit has been reached",
		},
		{
			name:   "Rate limit reported as a denial",
			status: http.StatusInternalServerError,
			code:   "access_denied",
			desc:   "global limit has been reached",
		},
		{
			name:   "Expired refresh token",
			status: http.StatusForbidden,
			code:   "invalid_grant",
			desc:   "the refresh token is expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newRefreshServer(t, func(tokenRequest) (int, string) {
				return tt.status, oauthErrorBody(tt.code, tt.desc)
			})
			setupRefreshConfig(t, server)

			_, err := RefreshAccessToken(server.URL, server.Client(), "refresh-token")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.code, "the server's own refusal must reach the caller unchanged")

			exchanges := server.exchanges()
			require.Len(t, exchanges, 1, "a refusal the scope cannot fix must not be sent a second time")
			assert.Contains(t, exchanges[0].Scope, "device:", "a retry has to have been possible for this to mean anything")
		})
	}
}

// TestRefreshAccessToken_FallbackIsObservable pins that the degrade leaves a
// trace. A fallback that always succeeds is indistinguishable from the path it
// replaced, so a permanently broken device scope would look exactly like a
// working one while presence quietly stopped being attributable.
func TestRefreshAccessToken_FallbackIsObservable(t *testing.T) {
	server := newRefreshServer(t, rejectDeviceScopeWith("invalid_scope"))
	setupRefreshConfig(t, server)
	t.Setenv(utils.DebugEnvVar, "1")

	var err error
	stderr := captureStderr(t, func() {
		_, err = RefreshAccessToken(server.URL, server.Client(), "refresh-token")
	})
	require.NoError(t, err)
	assert.Contains(t, stderr, "device scope")
	assert.Contains(t, stderr, "invalid_scope", "the server's own refusal code is what makes the line actionable")
}

// TestRefreshAccessToken_FallbackIsQuietWithoutTheDebugSwitch keeps the retry
// from becoming permanent noise: it fires on every refresh for installations
// that predate the scope, which is exactly the population that should not be
// warned on every command about something they cannot act on.
func TestRefreshAccessToken_FallbackIsQuietWithoutTheDebugSwitch(t *testing.T) {
	server := newRefreshServer(t, rejectDeviceScopeWith("invalid_scope"))
	setupRefreshConfig(t, server)
	t.Setenv(utils.DebugEnvVar, "")

	var err error
	stderr := captureStderr(t, func() {
		_, err = RefreshAccessToken(server.URL, server.Client(), "refresh-token")
	})
	require.NoError(t, err)
	assert.Empty(t, stderr)
}

// TestRefreshAccessToken_KeepsTheDeviceScopeWhenAccepted pins that the fallback
// costs nothing once a login has been made with the scope in the grant.
func TestRefreshAccessToken_KeepsTheDeviceScopeWhenAccepted(t *testing.T) {
	server := newRefreshServer(t, func(tokenRequest) (int, string) {
		return http.StatusOK, refreshSuccessBody
	})
	setupRefreshConfig(t, server)

	tokenRes, err := RefreshAccessToken(server.URL, server.Client(), "refresh-token")
	require.NoError(t, err)
	assert.Equal(t, "new-access-token", tokenRes.AccessToken)

	exchanges := server.exchanges()
	require.Len(t, exchanges, 1, "a refresh the server accepts must not be sent twice")
	assert.Contains(t, exchanges[0].Scope, "device:")
}

// TestRefreshAccessToken_ReportsTheFallbackFailure covers a scope refusal that
// turns out to be sitting on top of a second, unrelated problem—an expired or
// revoked token. The retry is entitled to run, since the first refusal really
// was a scope refusal, and the error the user ends up seeing must be the one
// describing the real problem rather than the scope refusal that triggered it.
func TestRefreshAccessToken_ReportsTheFallbackFailure(t *testing.T) {
	server := newRefreshServer(t, func(request tokenRequest) (int, string) {
		if strings.Contains(request.Scope, "device:") {
			return http.StatusForbidden, oauthErrorBody("invalid_scope", "the requested scope exceeds the original grant")
		}
		return http.StatusForbidden, oauthErrorBody("invalid_grant", "the refresh token is expired")
	})
	setupRefreshConfig(t, server)

	_, err := RefreshAccessToken(server.URL, server.Client(), "refresh-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_grant")
	assert.Len(t, server.exchanges(), 2)
}

// TestRefreshAccessToken_DoesNotReplayAnUnansweredExchange pins that the retry
// is limited to a refusal the server actually sent. When the reply cannot be
// read it is unknown whether the exchange was processed, and replaying it could
// spend a refresh token the server has already rotated.
func TestRefreshAccessToken_DoesNotReplayAnUnansweredExchange(t *testing.T) {
	server := newRefreshServer(t, func(tokenRequest) (int, string) {
		return http.StatusBadGateway, "<html>upstream is down</html>"
	})
	setupRefreshConfig(t, server)

	_, err := RefreshAccessToken(server.URL, server.Client(), "refresh-token")
	require.Error(t, err)
	assert.Len(t, server.exchanges(), 1, "an unreadable reply must not be retried")
}

// TestRefreshAccessToken_DoesNotRetryWhenNoDeviceScopeWasSent pins that a
// refusal is not retried when the retry would send the identical request. The
// identifier is unavailable here—an unreadable file is how that happens in the
// field—so there was never a device scope to drop.
func TestRefreshAccessToken_DoesNotRetryWhenNoDeviceScopeWasSent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not meaningful on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	server := newRefreshServer(t, func(tokenRequest) (int, string) {
		return http.StatusForbidden, oauthErrorBody("invalid_grant", "the refresh token is expired")
	})
	setupRefreshConfig(t, server)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	deviceIDPath := filepath.Join(homeDir, config.ConfigFileDir, config.DeviceIDFileName)
	require.NoError(t, os.WriteFile(deviceIDPath, []byte(testDeviceID+"\n"), 0600))
	require.NoError(t, os.Chmod(deviceIDPath, 0000))
	t.Cleanup(func() { _ = os.Chmod(deviceIDPath, 0600) })
	require.Empty(t, currentDeviceID(), "the identifier must be unavailable for this test to mean anything")

	_, err = RefreshAccessToken(server.URL, server.Client(), "refresh-token")
	require.Error(t, err)
	assert.Len(t, server.exchanges(), 1, "there was no device scope to drop, so the retry would be identical")
}
