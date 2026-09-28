package client

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	redirectAccessToken  = "access-token-value"
	redirectRefreshToken = "refresh-token-value"
)

type (
	// recordedRequest is what reached a redirect target.
	recordedRequest struct {
		Authorization string
		Body          string
	}

	// recordingServer answers every request with an empty JSON object and
	// keeps what each one carried.
	recordingServer struct {
		*httptest.Server

		mu       sync.Mutex
		requests []recordedRequest
	}
)

func newRecordingServer(t *testing.T, tls bool) *recordingServer {
	t.Helper()
	s := &recordingServer{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{
			Authorization: r.Header.Get("Authorization"),
			Body:          string(body),
		})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	if tls {
		s.Server = httptest.NewTLSServer(handler)
	} else {
		s.Server = httptest.NewServer(handler)
	}
	t.Cleanup(s.Close)
	return s
}

func (s *recordingServer) received() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

// redirectingServer answers every request with a redirect to target plus the
// request's own path.
func redirectingServer(t *testing.T, status int, target string) *httptest.Server {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, status)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// loginWithBearer writes a config holding an unexpired access token, so the
// client signs every request with it and never refreshes.
func loginWithBearer(t *testing.T, workspaceURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(
		workspaceURL, "my-workspace", "", "", redirectAccessToken, redirectRefreshToken, "", 3600, true,
	))
}

// The workspace is reached over TLS; a redirect that changes the scheme or the
// port leaves that origin even though the hostname stays 127.0.0.1.
func TestNewAlpaconAPIClient_RefusesRedirectOffTheWorkspaceOrigin(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		targetTLS bool
	}{
		{name: "307 from https to http", status: http.StatusTemporaryRedirect, targetTLS: false},
		{name: "302 from https to http", status: http.StatusFound, targetTLS: false},
		{name: "307 to another https port", status: http.StatusTemporaryRedirect, targetTLS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := newRecordingServer(t, tt.targetTLS)
			workspace := redirectingServer(t, tt.status, target.URL)
			loginWithBearer(t, workspace.URL)

			ac, err := NewAlpaconAPIClient()
			require.NoError(t, err)
			_, err = ac.SendGetRequest("/api/servers/servers/")

			assert.Empty(t, target.received(), "the redirect target must not be contacted")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "refusing redirect from "+workspace.URL+"/api/servers/servers/ to "+target.URL+"/api/servers/servers/")
		})
	}
}

// The refresh-token grant is a POST, and Go replays a POST body on a 307 to
// any host.
func TestNewAlpaconAPIClient_RefreshRefusesRedirectOffTheAuthServer(t *testing.T) {
	target := newRecordingServer(t, true)
	// Same listener as 127.0.0.1, a different hostname.
	targetURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	authServer := redirectingServer(t, http.StatusTemporaryRedirect, targetURL)

	workspace := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"auth0":{"method":"auth0","client_id":"client123","domain":"`+
			strings.TrimPrefix(authServer.URL, "https://")+
			`","audience":"https://api.example.com/","schema_name":"my-workspace"},"language":"en"}`)
	}))
	t.Cleanup(workspace.Close)

	t.Setenv("HOME", t.TempDir())
	// No expiry recorded, so the token counts as expired and construction refreshes.
	require.NoError(t, config.CreateConfig(
		workspace.URL, "my-workspace", "", "", redirectAccessToken, redirectRefreshToken, "", 0, true,
	))

	_, err := NewAlpaconAPIClient()

	assert.Empty(t, target.received(), "the refresh grant must not reach another host")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing redirect from "+authServer.URL+"/oauth/token/ to "+targetURL+"/oauth/token/")
}

func TestNewAlpaconAPIClient_FollowsSameOriginRedirect(t *testing.T) {
	var (
		mu    sync.Mutex
		auths []string
	)
	workspace := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path == "/api/servers/servers" {
			http.Redirect(w, r, "/api/servers/servers/", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(workspace.Close)
	loginWithBearer(t, workspace.URL)

	ac, err := NewAlpaconAPIClient()
	require.NoError(t, err)
	body, err := ac.SendGetRequest("/api/servers/servers")

	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"Bearer " + redirectAccessToken, "Bearer " + redirectAccessToken}, auths)
}

// A self-hosted workspace configured as http:// that upgrades itself to
// https:// on the same host keeps working. Both listeners sit behind the
// default ports of one hostname, which httptest cannot bind, so the dialer
// maps them.
func TestNewAlpaconAPIClient_FollowsUpgradeToHTTPS(t *testing.T) {
	const host = "workspace.example.test"
	secure := newRecordingServer(t, true)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+host+r.URL.Path, http.StatusMovedPermanently)
	}))
	t.Cleanup(plain.Close)
	loginWithBearer(t, "http://"+host)

	ac, err := NewAlpaconAPIClient()
	require.NoError(t, err)
	transport, ok := ac.HTTPClient.Transport.(*http.Transport)
	require.True(t, ok, "transport is %T", ac.HTTPClient.Transport)
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		switch addr {
		case host + ":80":
			addr = plain.Listener.Addr().String()
		case host + ":443":
			addr = secure.Listener.Addr().String()
		}
		return dialer.DialContext(ctx, network, addr)
	}

	_, err = ac.SendGetRequest("/api/servers/servers/")

	require.NoError(t, err)
	assert.Equal(t, []recordedRequest{{Authorization: "Bearer " + redirectAccessToken}}, secure.received())
}
