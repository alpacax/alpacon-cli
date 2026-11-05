package workspace

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: each case swaps os.Stderr through CaptureOutput.
func TestFetchKubernetesSurface(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		expected      bool
		expectWarning string
	}{
		{name: "workspace exposes kubernetes", status: http.StatusOK, body: `{"auth0":{"method":"auth0"},"surfaces":{"kubernetes":true}}`, expected: true},
		{name: "workspace without kubernetes", status: http.StatusOK, body: `{"auth0":{"method":"auth0"},"surfaces":{"kubernetes":false}}`},
		{name: "older server omits surfaces", status: http.StatusOK, body: `{"auth0":{"method":"auth0"}}`},
		{name: "env unreachable reads as no", status: http.StatusInternalServerError, body: `{}`, expectWarning: "stays hidden until you run 'alpacon login'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/auth/env/" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			var got bool
			_, stderr := testutil.CaptureOutput(t, func() {
				got = fetchKubernetesSurface(ts.URL, "next", false)
			})

			assert.Equal(t, tt.expected, got)
			if tt.expectWarning != "" {
				assert.Contains(t, stderr, tt.expectWarning)
			} else {
				assert.Empty(t, stderr)
			}
		})
	}
}

// Serial: each case sets HOME.
func TestCommitSwitch(t *testing.T) {
	errUnreachable := errors.New("connection refused")
	tests := []struct {
		name              string
		newSurface        bool
		verifyErr         error
		expectURL         string
		expectSchemaName  string
		renewed           *config.RenewedToken
		expectSurface     bool
		expectAccessToken string
		expectErrContains string
	}{
		{name: "switch records the new answer", newSurface: true, expectURL: "https://next.us1.alpacon.io", expectSchemaName: "next", expectSurface: true},
		{name: "switch saves the token renewed to find the workspace", renewed: &config.RenewedToken{AccessToken: "renewed-token", ExpiresIn: 3600},
			expectURL: "https://next.us1.alpacon.io", expectSchemaName: "next", expectAccessToken: "renewed-token"},
		{name: "failed connection keeps the renewed token", renewed: &config.RenewedToken{AccessToken: "renewed-token", ExpiresIn: 3600}, verifyErr: errUnreachable,
			expectURL: "https://prev-slug.us1.alpacon.io", expectSchemaName: "prev", expectSurface: true, expectAccessToken: "renewed-token",
			expectErrContains: `failed to connect to workspace "next": connection refused; reverted to "prev"`},
		{name: "switch to a workspace without kubernetes", expectURL: "https://next.us1.alpacon.io", expectSchemaName: "next"},
		{name: "failed connection restores the original answer", newSurface: false, verifyErr: errUnreachable,
			expectURL: "https://prev-slug.us1.alpacon.io", expectSchemaName: "prev", expectSurface: true,
			expectErrContains: `failed to connect to workspace "next": connection refused; reverted to "prev"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			require.NoError(t, config.CreateConfig("https://prev-slug.us1.alpacon.io", "prev-slug", "", "", "access-token", "refresh-token", "alpacon.io", 3600, false))
			require.NoError(t, config.SetWorkspaceEnv("https://prev-slug.us1.alpacon.io", "prev", true))
			orig, err := config.LoadConfig()
			require.NoError(t, err)

			err = commitSwitch(orig, "https://next.us1.alpacon.io", "next", tt.newSurface, tt.renewed, func() error { return tt.verifyErr })
			if tt.expectErrContains != "" {
				require.ErrorIs(t, err, tt.verifyErr)
				assert.Contains(t, err.Error(), tt.expectErrContains)
			} else {
				require.NoError(t, err)
			}

			cfg, err := config.LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.expectURL, cfg.WorkspaceURL)
			assert.Equal(t, tt.expectSchemaName, cfg.WorkspaceIdentity())
			assert.Equal(t, tt.expectSurface, cfg.KubernetesSurface)
			expectAccessToken := cmp.Or(tt.expectAccessToken, "access-token")
			assert.Equal(t, expectAccessToken, cfg.AccessToken)
		})
	}
}

// Serial: each case sets HOME.
func TestCommitSwitch_FailedConnectionLeavesALoginMadeMeanwhile(t *testing.T) {
	tests := []struct {
		name         string
		anotherLogin string
		wantErr      error
	}{
		{name: "login elsewhere", anotherLogin: "https://other.zz9.alpacon.io", wantErr: config.ErrWorkspaceChanged},
		{name: "another account on the workspace switched to", anotherLogin: "https://next.us1.alpacon.io", wantErr: config.ErrLoginChanged},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			require.NoError(t, config.CreateConfig("https://prev.us1.alpacon.io", "prev", "", "", "access-token", "refresh-token", "alpacon.io", 3600, false))
			orig, err := config.LoadConfig()
			require.NoError(t, err)
			var want config.Config
			verify := func() error {
				// Another shell logs in while this one checks the connection.
				require.NoError(t, config.CreateConfig(tt.anotherLogin, "other", "", "", "other-access", "other-refresh", "alpacon.io", 3600, false))
				cfg, loadErr := config.LoadConfig()
				require.NoError(t, loadErr)
				want = cfg
				return errors.New("connection refused")
			}

			err = commitSwitch(orig, "https://next.us1.alpacon.io", "next", false, nil, verify)

			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorContains(t, err, `failed to connect to "next": connection refused; left the config as another login or switch set it since`)
			cfg, err := config.LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, want, cfg)
		})
	}
}

func TestCommitSwitch_ConfigWriteFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	verified := false
	err := commitSwitch(config.Config{}, "https://next.us1.alpacon.io", "next", true, nil, func() error { verified = true; return nil })

	require.ErrorContains(t, err, "failed to update config")
	assert.False(t, verified, "no connection check runs when the config could not be written")
}

func workspacesToken(t *testing.T, names ...string) string {
	t.Helper()
	workspaces := make([]map[string]string, 0, len(names))
	for _, name := range names {
		workspaces = append(workspaces, map[string]string{"schema_name": name, "region": "us1"})
	}
	payload, err := json.Marshal(map[string]any{"https://alpacon.io/workspaces": workspaces})
	require.NoError(t, err)
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestResolveSwitchTarget(t *testing.T) {
	t.Parallel()
	rejected := fmt.Errorf("%w: invalid_grant", errRefreshRejected)
	unreachable := errors.New("connection refused")
	const notFound = `workspace "next" not found in your account`
	const signInAgain = notFound + ". Your workspace list reflects your last sign-in; run 'alpacon login' and retry"
	tests := []struct {
		name         string
		accessToken  string
		refreshToken string
		renewedToken string
		renewErr     error
		expectRenews int
		expectURL    string
		expectErr    string
		expectErrIs  []error
		expectNotIs  error
	}{
		{name: "workspace in the token resolves without renewal", accessToken: workspacesToken(t, "prev", "next"), refreshToken: "refresh-token",
			expectURL: "https://next.us1.alpacon.io"},
		{name: "workspace created after sign-in resolves after one renewal", accessToken: workspacesToken(t, "prev"), refreshToken: "refresh-token",
			renewedToken: workspacesToken(t, "prev", "next"), expectRenews: 1, expectURL: "https://next.us1.alpacon.io"},
		{name: "workspace missing after renewal suggests the name or a login", accessToken: workspacesToken(t, "prev"), refreshToken: "refresh-token",
			renewedToken: workspacesToken(t, "prev"), expectRenews: 1, expectErrIs: []error{workspace.ErrWorkspaceNotInToken},
			expectErr: notFound + " after renewing the access token. Check the name, or if the workspace was just created, run 'alpacon login' and retry"},
		{name: "unreadable renewed token suggests a login", accessToken: workspacesToken(t, "prev"), refreshToken: "refresh-token",
			renewedToken: "not-a-jwt", expectRenews: 1, expectNotIs: workspace.ErrWorkspaceNotInToken,
			expectErr: "failed to decode access token: invalid JWT format: expected 3 parts, got 1 after renewing the access token. Check the name, or if the workspace was just created, run 'alpacon login' and retry"},
		{name: "rejected refresh token asks for login", accessToken: workspacesToken(t, "prev"), refreshToken: "refresh-token",
			renewErr: rejected, expectRenews: 1, expectErrIs: []error{workspace.ErrWorkspaceNotInToken, errRefreshRejected},
			expectErr: notFound + ", and renewing the access token failed: the authentication server refused the refresh token: invalid_grant. Your workspace list reflects your last sign-in; run 'alpacon login' and retry"},
		{name: "transient renewal failure asks for a retry", accessToken: workspacesToken(t, "prev"), refreshToken: "refresh-token",
			renewErr: unreachable, expectRenews: 1, expectErrIs: []error{workspace.ErrWorkspaceNotInToken, unreachable},
			expectErr: notFound + ", and renewing the access token to look again failed. Please retry: connection refused"},
		{name: "no refresh token asks for login without renewal", accessToken: workspacesToken(t, "prev"),
			expectErrIs: []error{workspace.ErrWorkspaceNotInToken}, expectErr: signInAgain},
		{name: "unreadable token fails without renewal", accessToken: "not-a-jwt", refreshToken: "refresh-token",
			expectNotIs: workspace.ErrWorkspaceNotInToken, expectErr: "failed to decode access token: invalid JWT format: expected 3 parts, got 1"},
		{name: "token without a workspaces claim fails without renewal", accessToken: workspacesClaimlessToken(t), refreshToken: "refresh-token",
			expectNotIs: workspace.ErrWorkspaceNotInToken, expectErr: "workspaces claim not found in token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{AccessToken: tt.accessToken, RefreshToken: tt.refreshToken, BaseDomain: "alpacon.io"}
			renews := 0
			var issued *config.RenewedToken
			renew := func(got config.Config) (*config.RenewedToken, error) {
				renews++
				assert.Equal(t, cfg, got)
				if tt.renewErr != nil {
					return nil, tt.renewErr
				}
				issued = &config.RenewedToken{AccessToken: tt.renewedToken, ExpiresIn: 3600}
				return issued, nil
			}

			newURL, newName, renewed, err := resolveSwitchTarget(cfg, "next", renew)

			assert.Equal(t, tt.expectRenews, renews)
			if tt.expectErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.expectURL, newURL)
				assert.Equal(t, "next", newName)
				assert.Same(t, issued, renewed)
				return
			}
			require.EqualError(t, err, tt.expectErr)
			assert.Empty(t, newURL)
			assert.Empty(t, newName)
			assert.Nil(t, renewed)
			for _, target := range tt.expectErrIs {
				require.ErrorIs(t, err, target)
			}
			if tt.expectNotIs != nil {
				assert.NotErrorIs(t, err, tt.expectNotIs)
			}
		})
	}
}

func workspacesClaimlessToken(t *testing.T) string {
	t.Helper()
	return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"alice"}`)) + ".sig"
}
