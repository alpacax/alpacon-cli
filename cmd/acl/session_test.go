package acl

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWorkspaceURL = "https://ws1.us1.alpacon.io"
	testWorkspaceID  = "8b0f2c8e-1d4a-4c3b-9a55-0c1d2e3f4a5b"
)

func ledgerEnv() *auth0.AuthEnvResponse {
	return &auth0.AuthEnvResponse{
		Auth0:             auth0.Auth0Config{Method: "auth0", WorkspaceID: testWorkspaceID, SchemaName: "ws1"},
		CommandACLSurface: auth0.CommandACLSurfaceAI,
	}
}

// sessionSeams points every seam newLedgerSession reaches at the given values
// and restores them when the test ends. The config file lives in a temp HOME,
// since a refused token is cleared from it.
type sessionSeams struct {
	cfg          config.Config
	cfgErr       error
	env          *auth0.AuthEnvResponse
	authorized   string
	authorizeErr error
	authorizes   int
}

func installSeams(t *testing.T, s *sessionSeams) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	origLoad, origFetch, origAuthorize, origNow, origWorkspace := loadConfig, fetchAuthEnv, authorizeSigner, now, newWorkspaceClient
	t.Cleanup(func() {
		loadConfig, fetchAuthEnv, authorizeSigner, now, newWorkspaceClient = origLoad, origFetch, origAuthorize, origNow, origWorkspace
	})

	loadConfig = func() (config.Config, error) { return s.cfg, s.cfgErr }
	fetchAuthEnv = func(string, *http.Client) (*auth0.AuthEnvResponse, error) { return s.env, nil }
	authorizeSigner = func(config.Config, *auth0.AuthEnvResponse) (string, error) {
		s.authorizes++
		return s.authorized, s.authorizeErr
	}
	now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
}

// stubSigner serves /api/acl/me/ with the given status and body and records
// the Authorization header of each request.
func stubSigner(t *testing.T, status int, body string) *[]string {
	t.Helper()
	var auths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if r.URL.Path != "/api/acl/me/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	t.Setenv(client.SignerURLEnvVar, ts.URL)
	return &auths
}

func loggedIn(signerToken string) config.Config {
	cfg := config.Config{WorkspaceURL: testWorkspaceURL, WorkspaceName: "ws1", AccessToken: "access", RefreshToken: "refresh"}
	if signerToken != "" {
		cfg.SignerAccessToken = signerToken
		cfg.SignerAccessTokenExpiresAt = "2026-10-23T00:00:00Z"
		cfg.SignerWorkspaceURL = testWorkspaceURL
	}
	return cfg
}

const adminOfWorkspace = `{"is_ledger_admin": true, "workspace_ids": ["` + testWorkspaceID + `"]}`

func TestNewLedgerSession_UsesTheCachedSignerToken(t *testing.T) {
	seams := &sessionSeams{cfg: loggedIn("cached"), env: ledgerEnv()}
	installSeams(t, seams)
	auths := stubSigner(t, http.StatusOK, adminOfWorkspace)

	session, err := newLedgerSession()

	require.NoError(t, err)
	assert.Equal(t, testWorkspaceID, session.workspaceID)
	assert.Equal(t, 0, seams.authorizes, "a usable cached token needs no new authorization")
	assert.Equal(t, []string{"Bearer cached"}, *auths)
}

func TestNewLedgerSession_AuthorizesWhenNothingIsCached(t *testing.T) {
	seams := &sessionSeams{cfg: loggedIn(""), env: ledgerEnv(), authorized: "fresh"}
	installSeams(t, seams)
	auths := stubSigner(t, http.StatusOK, adminOfWorkspace)

	_, err := newLedgerSession()

	require.NoError(t, err)
	assert.Equal(t, 1, seams.authorizes)
	assert.Equal(t, []string{"Bearer fresh"}, *auths, "the workspace login's token never reaches the signer")
}

func TestNewLedgerSession_Refusals(t *testing.T) {
	upstream := ledgerEnv()
	upstream.CommandACLSurface = "upstream"
	olderServer := ledgerEnv()
	olderServer.CommandACLSurface = ""
	local := ledgerEnv()
	local.Auth0 = auth0.Auth0Config{Method: "local"}
	noWorkspaceID := ledgerEnv()
	noWorkspaceID.Auth0.WorkspaceID = ""
	apiToken := config.Config{WorkspaceURL: testWorkspaceURL, Token: "alpst-token"}

	tests := []struct {
		name    string
		seams   sessionSeams
		wantErr string
	}{
		{name: "not logged in", seams: sessionSeams{cfgErr: errors.New("no config")}, wantErr: "not logged in"},
		{name: "logged in with an API token", seams: sessionSeams{cfg: apiToken, env: ledgerEnv()}, wantErr: "not an API token"},
		{name: "workspace keeps its own ACLs", seams: sessionSeams{cfg: loggedIn("cached"), env: upstream}, wantErr: "does not use the command pre-approval ledger"},
		{name: "server predates the surface", seams: sessionSeams{cfg: loggedIn("cached"), env: olderServer}, wantErr: "does not use the command pre-approval ledger"},
		{name: "no browser sign-in", seams: sessionSeams{cfg: loggedIn("cached"), env: local}, wantErr: "browser sign-in"},
		{name: "no workspace id", seams: sessionSeams{cfg: loggedIn("cached"), env: noWorkspaceID}, wantErr: "too old"},
		{name: "authorization declined", seams: sessionSeams{cfg: loggedIn(""), env: ledgerEnv(), authorizeErr: errors.New("declined")}, wantErr: "declined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installSeams(t, &tt.seams)
			auths := stubSigner(t, http.StatusOK, adminOfWorkspace)

			_, err := newLedgerSession()

			require.ErrorContains(t, err, tt.wantErr)
			assert.Empty(t, *auths, "nothing reaches the signer")
		})
	}
}

func TestNewLedgerSession_NotACommandACLAdminHere(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "admin of nothing", body: `{"is_ledger_admin": false, "workspace_ids": []}`},
		{name: "workspace listed beside a false flag", body: `{"is_ledger_admin": false, "workspace_ids": ["` + testWorkspaceID + `"]}`},
		{name: "admin of another workspace", body: `{"is_ledger_admin": true, "workspace_ids": ["00000000-0000-0000-0000-000000000000"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installSeams(t, &sessionSeams{cfg: loggedIn("cached"), env: ledgerEnv()})
			stubSigner(t, http.StatusOK, tt.body)

			_, err := newLedgerSession()

			require.ErrorContains(t, err, "not a Command ACL admin of this workspace")
		})
	}
}

// A 401 from the signer means the cached token is no longer any good, so it is
// dropped and the next command authorizes again instead of failing the same way.
func TestNewLedgerSession_DropsARefusedSignerToken(t *testing.T) {
	seams := &sessionSeams{env: ledgerEnv()}
	installSeams(t, seams)
	require.NoError(t, config.CreateConfig(testWorkspaceURL, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	require.NoError(t, config.SaveSignerToken(cfg, "revoked", 86400))
	seams.cfg, err = config.LoadConfig()
	require.NoError(t, err)
	stubSigner(t, http.StatusUnauthorized, `{"detail": "Given token not valid for any token type", "code": "token_not_valid"}`)

	_, err = newLedgerSession()

	require.ErrorContains(t, err, "Run the command again to authorize once more")
	after, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, after.SignerAccessToken)
	assert.Equal(t, "access", after.AccessToken, "the workspace login is left as it was")
}

func TestErrorMessage(t *testing.T) {
	session := &ledgerSession{}
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "not an admin",
			status: http.StatusForbidden,
			body:   `{"detail": "You do not have permission to perform this action.", "code": "permission_denied"}`,
			want:   "Failed to list pre-approved commands: You do not have permission to perform this action. Only a Command ACL admin of this workspace may manage pre-approved commands.",
		},
		{
			name:   "validation",
			status: http.StatusBadRequest,
			body:   `{"subject": ["This is the last admin of the workspace."], "code": "last_admin"}`,
			want:   "Failed to list pre-approved commands: subject: This is the last admin of the workspace.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer ts.Close()
			t.Setenv(client.SignerURLEnvVar, ts.URL)
			_, err := client.NewSignerClient("token", false).SendGetRequest("/api/acl/entries/")
			require.Error(t, err)

			assert.Equal(t, tt.want, session.errorMessage("list pre-approved commands", err))
		})
	}
}

func TestErrorMessage_UnreachableSignerNamesTheOverride(t *testing.T) {
	t.Setenv(client.SignerURLEnvVar, "https://signer.example.com")
	message := (&ledgerSession{}).errorMessage("list pre-approved commands", errors.New("dial tcp: connection refused"))

	assert.Contains(t, message, "could not reach the command signer at https://signer.example.com")
	assert.Contains(t, message, client.SignerURLEnvVar)
}

func TestAuthorizationFailureMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "declined", err: auth0.ErrDeviceCodeDenied, want: "declined in the browser"},
		{name: "timed out", err: auth0.ErrDeviceCodeTimedOut, want: "timed out"},
		{name: "device code expired", err: auth0.ErrDeviceCodeExpired, want: "timed out"},
		{name: "other", err: fmt.Errorf("boom"), want: "failed: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			message := authorizationFailureMessage(tt.err)
			assert.Contains(t, message, tt.want)
			assert.NotContains(t, message, "alpacon login", "the remedy is running the acl command again")
		})
	}
}

// Name lookups go to the workspace the session was opened for. Once another
// shell has switched workspaces, resolving there would file a name from one
// workspace under the other's ledger.
func TestWorkspaceAPI_RefusesAWorkspaceTheSessionWasNotOpenedFor(t *testing.T) {
	installSeams(t, &sessionSeams{})
	newWorkspaceClient = func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{BaseURL: "https://ws2.us1.alpacon.io"}, nil
	}
	session := &ledgerSession{workspaceURL: testWorkspaceURL}

	_, err := session.workspaceAPI()
	require.ErrorContains(t, err, "workspace changed")

	_, err = session.resolveServiceToken("deploy-bot")
	require.ErrorContains(t, err, "workspace changed")
	_, err = session.resolveServerScope([]string{"web-01"})
	require.ErrorContains(t, err, "workspace changed")
}

func TestWorkspaceAPI_BuildsOneClientForTheSessionsWorkspace(t *testing.T) {
	installSeams(t, &sessionSeams{})
	builds := 0
	newWorkspaceClient = func() (*client.AlpaconClient, error) {
		builds++
		return &client.AlpaconClient{BaseURL: testWorkspaceURL}, nil
	}
	session := &ledgerSession{workspaceURL: testWorkspaceURL}

	first, err := session.workspaceAPI()
	require.NoError(t, err)
	second, err := session.workspaceAPI()
	require.NoError(t, err)
	assert.Same(t, first, second)
	assert.Equal(t, 1, builds)
}

// A token authorized for one login and workspace is not used once another
// shell has replaced either while the browser step ran.
func TestCacheSignerToken_FailsOnceTheConfigMoved(t *testing.T) {
	tests := []struct {
		name string
		move func(started config.Config) error
	}{
		{name: "another login", move: func(config.Config) error {
			return config.CreateConfig(testWorkspaceURL, "ws1", "", "", "access-2", "refresh-2", "alpacon.io", 3600, false)
		}},
		{name: "a workspace switch", move: func(started config.Config) error {
			return config.SwitchWorkspace(started, "https://ws2.us1.alpacon.io", "ws2", false)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			require.NoError(t, config.CreateConfig(testWorkspaceURL, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
			started, err := config.LoadConfig()
			require.NoError(t, err)
			require.NoError(t, tt.move(started))

			token, err := cacheSignerToken(started, "signer", 86400)

			require.ErrorContains(t, err, "Run the command again")
			assert.Empty(t, token)
		})
	}
}

func TestCacheSignerToken_ReturnsTheSavedToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(testWorkspaceURL, "ws1", "", "", "access", "refresh", "alpacon.io", 3600, false))
	started, err := config.LoadConfig()
	require.NoError(t, err)

	token, err := cacheSignerToken(started, "signer", 86400)

	require.NoError(t, err)
	assert.Equal(t, "signer", token)
}

// The override may carry credentials in its userinfo or query; neither the
// message nor the transport error it quotes may print them.
func TestErrorMessage_RedactsTheSignerURL(t *testing.T) {
	override := "https://user:secret@signer.example.com/base?token=abc"
	t.Setenv(client.SignerURLEnvVar, override)
	transportErr := &url.Error{Op: "Get", URL: override + "/api/acl/me/", Err: errors.New("connection refused")}

	message := (&ledgerSession{}).errorMessage("list pre-approved commands", transportErr)

	assert.NotContains(t, message, "secret")
	assert.NotContains(t, message, "token=abc")
	assert.Contains(t, message, "https://signer.example.com/base")
}
