package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reported failure, end to end: an access token that expires while a human
// is deciding. It is deterministic rather than transient—the longer the approval
// takes, the more certain the 401—so --wait used to be least reliable exactly
// when it mattered most.
//
// Nothing below the CLI's own HTTP client is stubbed here: the fake workspace
// serves the auth env and the token endpoint too, so the renewal that keeps the
// wait alive is the real one.
func TestPollForApproval_SurvivesATokenExpiryMidWait(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	polls, refreshes := 0, 0
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()

	mux.HandleFunc("/api/auth/env/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth0":{"method":"auth0","client_id":"cli","domain":"` + strings.TrimPrefix(ts.URL, "https://") + `"}}`))
	})
	mux.HandleFunc("/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","expires_in":3600,"token_type":"Bearer"}`))
	})
	mux.HandleFunc("/api/work-sessions/sessions/", func(w http.ResponseWriter, r *http.Request) {
		polls++
		w.Header().Set("Content-Type", "application/json")
		// Anything but the renewed token is the one that expired.
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"Authentication credentials were not provided."}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ws-uuid","status":"approved"}`))
	})

	writeTestConfig(t, home, config.Config{
		WorkspaceURL: ts.URL,
		AccessToken:  "stale",
		RefreshToken: "r1",
		BaseDomain:   "alpacon.io",
	})

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, AccessToken: "stale"}

	session, err := pollForApproval(ac, "ws-uuid", false, time.Millisecond, 5*time.Second)

	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, "approved", session.Status)
	assert.Equal(t, 1, refreshes, "one expiry must cost one refresh-token grant")
	assert.Equal(t, 2, polls, "the expired poll must be retried, not reported as a failed wait")
}

func writeTestConfig(t *testing.T, home string, cfg config.Config) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	dir := filepath.Join(home, config.ConfigFileDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), raw, 0600))
}
