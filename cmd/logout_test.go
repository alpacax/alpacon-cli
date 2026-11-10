package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: each case swaps os.Stderr through CaptureOutput.
func TestReportLogout(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStderr string
	}{
		{name: "config removed", wantStderr: "Logout succeeded!"},
		{name: "another shell logged in meanwhile", err: fmt.Errorf("delete: %w", config.ErrLoginChanged),
			wantStderr: "Logged out, but kept the config: another shell logged in since this command started."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr := testutil.CaptureOutput(t, func() {
				reportLogout(tt.err)
			})

			assert.Contains(t, stderr, tt.wantStderr)
		})
	}
}

type blacklistRefusal struct{ hint string }

func (e blacklistRefusal) Error() string {
	return "permission denied (HTTP 403, code: permission_denied)"
}
func (e blacklistRefusal) ErrorCode() string   { return "permission_denied" }
func (e blacklistRefusal) ErrorSource() string { return "" }
func (e blacklistRefusal) ErrorHint() string   { return e.hint }

func TestBlacklistFailure(t *testing.T) {
	t.Parallel()
	const hint = "This account or credential does not have permission for this action."

	hinted := blacklistFailure(blacklistRefusal{hint: hint})
	assert.Equal(t, "Failed to set black list: permission denied (HTTP 403, code: permission_denied).", hinted.Error())
	assert.Equal(t, hint, utils.RefusalHint(hinted))

	// Without a hint the retry advice stays.
	plain := blacklistFailure(errors.New("request failed"))
	assert.Equal(t, "Failed to set black list. Please try again later.", plain.Error())
	assert.Empty(t, utils.RefusalHint(plain))
}

// Serial: the config lives under HOME, which t.Setenv sets.
func TestRunLogout_RemovesCredentialsWhenTheSessionIsUnusable(t *testing.T) {
	tests := []struct {
		name         string
		unreachable  bool
		token        string
		accessToken  string
		refreshToken string
		expiresIn    int
		wantPath     string
		wantNoPath   []string
	}{
		{name: "api key and unreachable workspace", unreachable: true, token: "fake-api-key"},
		{name: "expired access token and unreachable workspace", unreachable: true, accessToken: "expired", refreshToken: "refresh"},
		{name: "expired access token without refresh token", accessToken: "expired", wantNoPath: []string{blacklistURL, "/api/auth/logout/"}},
		{name: "valid access token and failing blacklist", accessToken: "access", refreshToken: "refresh", expiresIn: 3600, wantPath: blacklistURL, wantNoPath: []string{"/api/auth/logout/"}},
		{name: "api key and failing logout request", token: "fake-api-key", wantPath: "/api/auth/logout/", wantNoPath: []string{blacklistURL}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				http.Error(w, `{"detail":"boom"}`, http.StatusInternalServerError)
			}))
			workspaceURL := ts.URL
			if tt.unreachable {
				ts.Close()
			} else {
				t.Cleanup(ts.Close)
			}

			home := t.TempDir()
			setTestHome(t, home)
			require.NoError(t, config.CreateConfig(workspaceURL, "my-workspace", tt.token, "", tt.accessToken, tt.refreshToken, "", tt.expiresIn, false))
			login, err := config.LoadConfig()
			require.NoError(t, err)

			var runErr error
			_, stderr := testutil.CaptureOutput(t, func() { runErr = runLogout(login) })

			require.NoError(t, runErr)
			assert.NoFileExists(t, filepath.Join(home, ".alpacon", "config.json"))
			assert.Contains(t, stderr, "server-side session may still be valid")
			mu.Lock()
			defer mu.Unlock()
			if tt.wantPath != "" {
				assert.Contains(t, paths, tt.wantPath)
			}
			for _, p := range tt.wantNoPath {
				assert.NotContains(t, paths, p)
			}
		})
	}
}

// Serial: the config lives under HOME, which t.Setenv sets.
func TestRunLogout_RemovesCredentialsWhenTheAuthServerRefuses(t *testing.T) {
	tests := []struct {
		name         string
		expiresIn    int
		wantPaths    []string
		wantNoPaths  []string
		wantWarnings []string
	}{
		{
			name:         "refresh token rejected",
			wantPaths:    []string{"/oauth/token", "/oauth/revoke"},
			wantNoPaths:  []string{strings.TrimSuffix(blacklistURL, "/")},
			wantWarnings: []string{"Failed to create Alpacon API client", "failed to revoke token"},
		},
		{
			name:         "blacklist and revoke refused",
			expiresIn:    3600,
			wantPaths:    []string{strings.TrimSuffix(blacklistURL, "/"), "/oauth/revoke"},
			wantNoPaths:  []string{"/oauth/token"},
			wantWarnings: []string{"Failed to set black list", "failed to revoke token"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			var ts *httptest.Server
			ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, strings.TrimSuffix(r.URL.Path, "/"))
				mu.Unlock()
				switch strings.TrimSuffix(r.URL.Path, "/") {
				case "/api/auth/env":
					domain := strings.TrimPrefix(ts.URL, "https://")
					_, _ = fmt.Fprintf(w, `{"auth0":{"method":"auth0","client_id":"cid","domain":%q}}`, domain)
				case "/oauth/token":
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Unknown or invalid refresh token."}`))
				default:
					http.Error(w, `{"detail":"refused"}`, http.StatusBadRequest)
				}
			}))
			t.Cleanup(ts.Close)

			home := t.TempDir()
			setTestHome(t, home)
			require.NoError(t, config.CreateConfig(ts.URL, "my-workspace", "", "", "access", "refresh", "", tt.expiresIn, true))
			login, err := config.LoadConfig()
			require.NoError(t, err)

			var runErr error
			_, stderr := testutil.CaptureOutput(t, func() { runErr = runLogout(login) })

			require.NoError(t, runErr)
			assert.NoFileExists(t, filepath.Join(home, ".alpacon", "config.json"))
			for _, w := range tt.wantWarnings {
				assert.Contains(t, stderr, w)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, p := range tt.wantPaths {
				assert.Contains(t, paths, p)
			}
			for _, p := range tt.wantNoPaths {
				assert.NotContains(t, paths, p)
			}
		})
	}
}

func TestRunLogout_KeepsALoginAnotherShellMade(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	require.NoError(t, config.CreateConfig("https://my-workspace.invalid", "my-workspace", "other-shell-key", "", "", "", "", 0, false))
	stale := config.Config{WorkspaceURL: "https://my-workspace.invalid", Token: "fake-api-key"}

	var runErr error
	_, _ = testutil.CaptureOutput(t, func() { runErr = runLogout(stale) })

	require.ErrorIs(t, runErr, config.ErrLoginChanged)
	_, err := os.Stat(filepath.Join(home, ".alpacon", "config.json"))
	assert.NoError(t, err)
}
