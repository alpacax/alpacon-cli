package kube

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type usernameServer struct {
	*sessionServer
	account       string
	accountReads  atomic.Int32
	usernamePosts atomic.Int32
}

func (s *usernameServer) handler(t *testing.T) http.Handler {
	t.Helper()
	sessions := s.sessionServer.handler(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/iam/users/-/":
			s.accountReads.Add(1)
			if s.account == "" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, s.account)
		case r.Method == http.MethodPost && r.URL.Path == "/api/iam/username/":
			s.usernamePosts.Add(1)
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"username":"alice"}`, string(body))
			_, _ = io.WriteString(w, `{"username":"alice"}`)
		default:
			sessions.ServeHTTP(w, r)
		}
	})
}

// Serial: swaps the username prompt and disables browser launches.
func TestOpenKubeConfigUsernameRecovery(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name        string
		refusals    []string
		account     string
		reads       int32
		prompts     int32
		creates     int
		mfaLinks    int
		interrupt   bool
		wantRefusal string
	}{
		{name: "username required", refusals: []string{utils.UsernameRequired}, prompts: 1, creates: 2},
		{name: "older server with null username", refusals: []string{utils.KubeSessionPrincipalNotSupported},
			account: `{"username":null}`, reads: 1, prompts: 1, creates: 2},
		{name: "older server with set username", refusals: []string{utils.KubeSessionPrincipalNotSupported},
			account: `{"username":"system:reserved"}`, reads: 1, creates: 1, wantRefusal: utils.KubeSessionPrincipalNotSupported},
		{name: "older server with empty username", refusals: []string{utils.KubeSessionPrincipalNotSupported},
			account: `{"username":""}`, reads: 1, creates: 1, wantRefusal: utils.KubeSessionPrincipalNotSupported},
		{name: "older server with missing username field", refusals: []string{utils.KubeSessionPrincipalNotSupported},
			account: `{}`, reads: 1, creates: 1, wantRefusal: utils.KubeSessionPrincipalNotSupported},
		{name: "older server with failed account read", refusals: []string{utils.KubeSessionPrincipalNotSupported},
			reads: 1, creates: 1, wantRefusal: utils.KubeSessionPrincipalNotSupported},
		{name: "MFA then username", refusals: []string{utils.AuthMFARequired, utils.UsernameRequired},
			prompts: 1, creates: 3, mfaLinks: 1},
		{name: "username then MFA", refusals: []string{utils.UsernameRequired, utils.AuthMFARequired},
			prompts: 1, creates: 3, mfaLinks: 1},
		{name: "username refused again", refusals: []string{utils.UsernameRequired, utils.UsernameRequired},
			prompts: 1, creates: 2, wantRefusal: utils.UsernameRequired},
		{name: "interrupt during username setup", refusals: []string{utils.UsernameRequired},
			prompts: 1, creates: 1, interrupt: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			srv := &usernameServer{sessionServer: newSessionServer(), account: tt.account}
			srv.createRefusals = tt.refusals
			ac := &client.AlpaconClient{
				HTTPClient:    testutil.HandlerClient(srv.handler(t)),
				BaseURL:       testutil.StubBaseURL,
				WorkspaceName: "my-workspace",
			}
			interrupted := false
			var prompts int32
			oldPrompt := handleKubeUsernameRequired
			t.Cleanup(func() { handleKubeUsernameRequired = oldPrompt })
			handleKubeUsernameRequired = func(ac *client.AlpaconClient, stopped func() bool) (*iam.SetUsernameResponse, error) {
				prompts++
				// The prompt must see the command's interrupt, or a name typed after
				// a first Ctrl+C would still be sent.
				if assert.NotNil(t, stopped) {
					interrupted = true
					assert.True(t, stopped())
					interrupted = false
				}
				assert.NoFileExists(t, path+".lock")
				result, err := iam.SetUsername(ac, "alice")
				interrupted = tt.interrupt
				return result, err
			}

			synctest.Test(t, func(t *testing.T) {
				hold := openHold(t, path)
				result, err := openKubeConfig(ac, hold, kubeConfigOptions{
					clusterName: "prod-east", kubeconfig: path, interrupted: func() bool { return interrupted },
				})
				switch {
				case tt.interrupt:
					require.ErrorIs(t, err, errInterrupted)
				case tt.wantRefusal != "":
					require.Error(t, err)
					assert.Equal(t, tt.wantRefusal, utils.StructuredErrorCode(err))
					if tt.wantRefusal == utils.KubeSessionPrincipalNotSupported {
						message := kubeConfigError(err, "prod-east", "").Error()
						assert.Contains(t, message, "this account's IAM username cannot be used as a kube session's identity")
						assert.Contains(t, message, "be at most 150 characters")
					}
				default:
					require.NoError(t, err)
					assert.Equal(t, testSessionID, result.KubeSessionID)
					assert.FileExists(t, path+".lock")
				}
			})

			assert.Equal(t, tt.prompts, prompts)
			assert.Equal(t, tt.prompts, srv.usernamePosts.Load())
			assert.Equal(t, tt.reads, srv.accountReads.Load())
			assert.Equal(t, tt.creates, srv.creates())
			assert.Len(t, srv.mfaLinkQueries(), tt.mfaLinks)
			assert.Zero(t, srv.closes())
			if tt.interrupt || tt.wantRefusal != "" {
				assert.NoFileExists(t, path)
			}
			assert.NoFileExists(t, path+".lock")
		})
	}
}

// Serial: replaces stdin with a pipe so the prompt sees a non-interactive shell.
func TestKubeUsernameRequiredNonInteractive(t *testing.T) {
	in, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	oldStdin := os.Stdin
	os.Stdin = in
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = in.Close()
	})

	for _, command := range []string{"config", "login"} {
		t.Run(command, func(t *testing.T) {
			srv := &usernameServer{sessionServer: newSessionServer()}
			srv.createRefusals = []string{utils.UsernameRequired}
			ts := httptest.NewServer(srv.handler(t))
			defer ts.Close()
			t.Setenv("HOME", t.TempDir())
			require.NoError(t, config.CreateConfig(ts.URL, "ws", "", "", "access-token", "", "", 0, false))
			path := filepath.Join(t.TempDir(), "config")
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			ac.SetAccessToken("access-token")

			stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command {
				return NewKubeCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
			}, command, "prod-east", "--work-session", testSessionID, "--kubeconfig", path)

			assert.Equal(t, utils.ExitCodeGeneralError, code, stderr)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, `"error_code":"user_username_required"`)
			assert.Contains(t, stderr, "alpacon username set <name>")
			assert.Equal(t, 1, srv.creates())
			assert.Zero(t, srv.accountReads.Load())
			assert.Zero(t, srv.usernamePosts.Load())
			assert.NoFileExists(t, path)
		})
	}
}
