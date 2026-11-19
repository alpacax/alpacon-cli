package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const (
	testSessionID = "6b0f3c1e-2d4a-4b8e-9f71-3c5a2e9d8b10"
	testToken     = "channel-token-value"
	testProxyURL  = "https://my-workspace.us1.alpacon.io/kube/c-prod"
)

// sessionServer answers the cluster lookup, session create and session close,
// and records what it was sent.
type sessionServer struct {
	lookupStatus int // 0 answers the lookup with prod-east
	createStatus int // 0 answers 201 with the session below
	createCode   string
	createHint   string
	proxyURL     string
	token        string
	closeStatus  int // 0 answers 200
	// rbacDriftWarning is the created session's rbac_drift_warning as raw JSON;
	// "" leaves the field out, as a server older than it does.
	rbacDriftWarning string
	// mfaCreates is how many creates, from the first, answer 401
	// auth_mfa_required as a workspace requiring MFA for kube exec does. The
	// createStatus answer, or the session, comes after them.
	mfaCreates int
	// onCreate, when set, runs as create number call (from 1) arrives, before
	// it is answered.
	onCreate      func(call int)
	mfaLinkStatus int // 0 answers the MFA link request with a link

	mu          sync.Mutex
	createBody  map[string]any
	createCalls int
	closeCalls  []string
	mfaLinks    []url.Values
}

func newSessionServer() *sessionServer {
	return &sessionServer{proxyURL: testProxyURL, token: testToken}
}

func (s *sessionServer) start(t *testing.T) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(s.handler(t))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
}

// startInMemory serves the same answers with no socket, so a test of the MFA
// wait can run under testing/synctest. The client names a workspace, as every
// MFA link must.
func (s *sessionServer) startInMemory(t *testing.T) *client.AlpaconClient {
	t.Helper()
	return &client.AlpaconClient{
		HTTPClient:    testutil.HandlerClient(s.handler(t)),
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
	}
}

func (s *sessionServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/kubernetes/clusters/":
			if s.lookupStatus != 0 {
				w.WriteHeader(s.lookupStatus)
				_, _ = w.Write([]byte(`{"detail":"Not found."}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"count":1,"current":1,"next":null,"previous":null,"last":1,"results":[%s]}`, prodCluster)
		case r.Method == http.MethodPost && r.URL.Path == "/api/kubernetes/sessions/":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			s.mu.Lock()
			s.createBody = body
			s.createCalls++
			call := s.createCalls
			s.mu.Unlock()
			if s.onCreate != nil {
				s.onCreate(call)
			}
			if call <= s.mfaCreates {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"auth_mfa_required","source":"kube_exec"}`))
				return
			}
			if s.createStatus != 0 {
				w.WriteHeader(s.createStatus)
				_, _ = fmt.Fprintf(w, `{"code":%q,"hint":%q}`, s.createCode, s.createHint)
				return
			}
			warning := ""
			if s.rbacDriftWarning != "" {
				warning = `"rbac_drift_warning":` + s.rbacDriftWarning + `,`
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":%q,"cluster":"c-prod","work_session":null,"user":null,
				"impersonate_user":"alice","impersonate_groups":[],"client_type":"cli",%s
				"token_expired_at":"2026-10-08T09:40:00Z","closed_at":null,"added_at":"2026-10-08T07:40:00Z",
				"updated_at":"2026-10-08T07:40:00Z","channel_id":"ch-1","channel_token":%q,"proxy_url":%q}`,
				testSessionID, warning, s.token, s.proxyURL)
		case r.Method == http.MethodPost && r.URL.Path == "/api/kubernetes/sessions/"+testSessionID+"/close/":
			s.mu.Lock()
			s.closeCalls = append(s.closeCalls, r.URL.Path)
			s.mu.Unlock()
			if s.closeStatus != 0 {
				w.WriteHeader(s.closeStatus)
				_, _ = w.Write([]byte(`{"detail":"failed"}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/work-sessions/sessions/"):
			// A WorkSession read that fails leaves kube login to the server's judgment.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/auth0/mfa/":
			s.mu.Lock()
			s.mfaLinks = append(s.mfaLinks, r.URL.Query())
			s.mu.Unlock()
			if s.mfaLinkStatus != 0 {
				w.WriteHeader(s.mfaLinkStatus)
				_, _ = w.Write([]byte(`{"code":"workspace_not_found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"mfa_url":"https://auth.example.com/mfa?ticket=t-1"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
}

func (s *sessionServer) sentBody() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createBody
}

func (s *sessionServer) creates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCalls
}

func (s *sessionServer) closes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.closeCalls)
}

func (s *sessionServer) mfaLinkQueries() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.mfaLinks)
}

func openHold(t *testing.T, path string) *kubeconfigHold {
	t.Helper()
	hold, err := openKubeconfigHold(path)
	require.NoError(t, err)
	t.Cleanup(hold.close)
	return hold
}

type testKubeconfig struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string         `yaml:"name"`
		Cluster map[string]any `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string         `yaml:"name"`
		User map[string]any `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string         `yaml:"name"`
		Context map[string]any `yaml:"context"`
	} `yaml:"contexts"`
}

func readKubeconfig(t *testing.T, path string) testKubeconfig {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg testKubeconfig
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	return cfg
}

func TestOpenKubeConfigWritesTheSessionContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		workSessionID string
		proxyPort     int
		insecure      bool
	}{
		{name: "direct with a work session", workSessionID: "ws-1"},
		{name: "no work session sent when none is active"},
		{name: "through the local proxy", workSessionID: "ws-1", proxyPort: 18444},
		{name: "insecure workspace", workSessionID: "ws-1", insecure: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newSessionServer()
			ac := srv.start(t)
			ac.Insecure = tt.insecure
			path := filepath.Join(t.TempDir(), ".kube", "config")

			result, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{
				clusterName: "prod-east", workSessionID: tt.workSessionID, kubeconfig: path, proxyPort: tt.proxyPort,
			})
			require.NoError(t, err)

			expectedBody := map[string]any{"cluster": "c-prod", "client_type": "cli"}
			if tt.workSessionID != "" {
				expectedBody["work_session"] = tt.workSessionID
			}
			assert.Equal(t, expectedBody, srv.sentBody())
			assert.Equal(t, &kubeConfigResult{
				Context:        "alpacon-prod-east",
				Kubeconfig:     path,
				KubeSessionID:  testSessionID,
				ClusterID:      "c-prod",
				WorkSessionID:  tt.workSessionID,
				ProxyURL:       testProxyURL,
				clusterName:    "prod-east",
				TokenExpiresAt: time.Date(2026, 10, 8, 9, 40, 0, 0, time.UTC),
			}, result)
			assert.Zero(t, srv.closes())

			cfg := readKubeconfig(t, path)
			assert.Equal(t, "alpacon-prod-east", cfg.CurrentContext)
			require.Len(t, cfg.Clusters, 1)
			expectedCluster := map[string]any{"server": testProxyURL}
			if tt.proxyPort != 0 {
				expectedCluster["proxy-url"] = "http://127.0.0.1:18444"
			}
			if tt.insecure {
				expectedCluster["insecure-skip-tls-verify"] = true
			}
			assert.Equal(t, expectedCluster, cfg.Clusters[0].Cluster)
			require.Len(t, cfg.Users, 1)
			assert.Equal(t, map[string]any{"token": testToken}, cfg.Users[0].User)
			require.Len(t, cfg.Contexts, 1)
			assert.Equal(t, map[string]any{"cluster": "alpacon-prod-east", "user": "alpacon-prod-east"}, cfg.Contexts[0].Context)
		})
	}
}

func TestOpenKubeConfigReportsThePreviousContext(t *testing.T) {
	t.Parallel()
	srv := newSessionServer()
	ac := srv.start(t)
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte(`apiVersion: v1
kind: Config
clusters:
- name: kind-dev
  cluster:
    server: https://127.0.0.1:6443
users:
- name: kind-dev
  user:
    token: other
contexts:
- name: kind-dev
  context:
    cluster: kind-dev
    user: kind-dev
current-context: kind-dev
`), 0600))

	result, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
	require.NoError(t, err)

	assert.Equal(t, "kind-dev", result.PreviousContext)
	cfg := readKubeconfig(t, path)
	assert.Equal(t, "alpacon-prod-east", cfg.CurrentContext)
	assert.Len(t, cfg.Contexts, 2)
}

func TestOpenKubeConfigLookupFailureOpensNoSession(t *testing.T) {
	t.Parallel()
	srv := newSessionServer()
	srv.lookupStatus = http.StatusNotFound
	ac := srv.start(t)
	path := filepath.Join(t.TempDir(), "config")

	_, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	var lookupErr *clusterLookupError
	require.ErrorAs(t, err, &lookupErr)
	assert.True(t, kubernetes.IsSurfaceOff(lookupErr.err), "a 404 on the cluster list reads as the surface off: %v", err)
	assert.Zero(t, srv.creates())
	assert.NoFileExists(t, path)
}

func TestOpenKubeConfigCreateRefusalWritesNothing(t *testing.T) {
	t.Parallel()
	srv := newSessionServer()
	srv.createStatus = http.StatusForbidden
	srv.createCode = utils.WorkSessionRequired
	ac := srv.start(t)
	path := filepath.Join(t.TempDir(), "config")

	_, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	var createErr *sessionCreateError
	require.ErrorAs(t, err, &createErr)
	code, _ := utils.ParseErrorResponse(createErr.err)
	assert.Equal(t, utils.WorkSessionRequired, code)
	assert.Zero(t, srv.closes())
	assert.NoFileExists(t, path)
}

// A session the command cannot write is closed: its token is never handed out
// again, so nothing could use it, and it would stay open until it expired.
func TestOpenKubeConfigClosesASessionItCannotWrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		proxyURL  string
		token     string
		proxyPort int
		message   string
	}{
		{name: "plain http with the local proxy", proxyURL: "http://ws.example.com/kube/c-prod", token: testToken, proxyPort: 18443, message: "plain http"},
		{name: "empty proxy URL", proxyURL: "", token: testToken, message: "unusable proxy URL"},
		{name: "proxy URL without a host", proxyURL: "https:///kube/c-prod", token: testToken, message: "unusable proxy URL"},
		{name: "unsupported scheme", proxyURL: "ftp://ws.example.com/kube/c-prod", token: testToken, message: "unusable proxy URL"},
		{name: "no token", proxyURL: testProxyURL, token: "", message: "no token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newSessionServer()
			srv.proxyURL = tt.proxyURL
			srv.token = tt.token
			ac := srv.start(t)
			path := filepath.Join(t.TempDir(), "config")

			_, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{
				clusterName: "prod-east", kubeconfig: path, proxyPort: tt.proxyPort,
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
			assert.Contains(t, err.Error(), "the kube session was closed")
			assert.Equal(t, 1, srv.closes())
			assert.NoFileExists(t, path)
		})
	}
}

func TestOpenKubeConfigReportsAFailedClose(t *testing.T) {
	t.Parallel()
	srv := newSessionServer()
	srv.token = ""
	srv.closeStatus = http.StatusInternalServerError
	ac := srv.start(t)
	path := filepath.Join(t.TempDir(), "config")

	_, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "closing the kube session "+testSessionID+" also failed")
	assert.Equal(t, 1, srv.closes())
}

func TestOpenKubeConfigClosesTheSessionWhenTheWriteFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("a read-only directory does not stop a write on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	srv := newSessionServer()
	ac := srv.start(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	hold := openHold(t, path)
	// The directory turns read-only after the file was opened and checked, so
	// only the write itself fails.
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	_, err := openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write "+path)
	assert.Equal(t, 1, srv.closes())
}

func TestPlainHTTPProxyURLIsWrittenAndFlagged(t *testing.T) {
	t.Parallel()
	srv := newSessionServer()
	srv.proxyURL = "http://ws.example.com/kube/c-prod"
	ac := srv.start(t)
	path := filepath.Join(t.TempDir(), "config")

	result, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	require.NoError(t, err)
	assert.True(t, result.plaintext, "an http proxy URL must be flagged: %s", result.ProxyURL)
	assert.Zero(t, srv.closes())
}

func TestKubeSessionErrorHint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{name: "cluster not connected", code: utils.ClusterNotConnected, expected: "'alpacon kube describe prod-east'"},
		{name: "channel open failed", code: utils.KubeSessionChannelOpenFailed, expected: "try again"},
		{name: "no user name", code: utils.KubeSessionPrincipalNotSupported, expected: "log in as a user"},
		{name: "cluster rbac drift", code: utils.ClusterRBACDrift, expected: "cluster prod-east has RBAC bindings outside Alpacon"},
		{name: "other code", code: utils.APITokenACLNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newSessionServer()
			srv.createStatus = http.StatusBadRequest
			srv.createCode = tt.code
			ac := srv.start(t)
			path := filepath.Join(t.TempDir(), "config")

			_, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
			require.Error(t, err)

			hint := kubeSessionErrorHint(err, "prod-east")
			if tt.expected == "" {
				assert.Empty(t, hint)
				return
			}
			assert.Contains(t, hint, tt.expected)
		})
	}
	assert.Empty(t, kubeSessionErrorHint(errors.New("plain failure"), "prod-east"))
}

// hintedKubeError is a coded kube refusal carrying the server's hint.
type hintedKubeError struct{ code, hint string }

func (e hintedKubeError) Error() string       { return "request refused (code: " + e.code + ")" }
func (e hintedKubeError) ErrorCode() string   { return e.code }
func (e hintedKubeError) ErrorSource() string { return "" }
func (e hintedKubeError) ErrorHint() string   { return e.hint }

func TestKubeSessionErrorHint_DefersToTheServerHint(t *testing.T) {
	t.Parallel()
	hinted := hintedKubeError{code: utils.ClusterNotConnected, hint: "Start the cluster's agent and try again."}
	assert.Empty(t, kubeSessionErrorHint(hinted, "prod-east"))

	plain := hintedKubeError{code: utils.ClusterNotConnected}
	assert.Contains(t, kubeSessionErrorHint(plain, "prod-east"), "is not connected")
}

func TestKubeConfigRBACDriftRefusal(t *testing.T) {
	t.Parallel()
	const bindingNotice = "The cluster's agent logs an external_grant line for each such binding so the cluster admin can find them there."
	// The server refuses with 400 and no hint. The client keeps a hint only on
	// a 402/403/405/429, so the hint case answers 403 to reach that rule.
	tests := []struct {
		name   string
		status int
		hint   string
	}{
		{name: "CLI guidance", status: http.StatusBadRequest},
		{name: "server guidance", status: http.StatusForbidden, hint: "Contact a cluster admin to review the bindings."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newSessionServer()
			srv.createStatus, srv.createCode, srv.createHint = tt.status, utils.ClusterRBACDrift, tt.hint
			ac := srv.start(t)
			path := filepath.Join(t.TempDir(), "config")

			stdout, stderr, code := cmdtest.RunCaptured(func(ios *utils.IOStreams) *cobra.Command {
				return NewKubeCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
			}, "config", "prod-east", "--kubeconfig", path)

			assert.Equal(t, utils.ExitCodeGeneralError, code, stderr)
			assert.Empty(t, stdout)
			if tt.hint == "" {
				assert.Contains(t, stderr, "cluster prod-east has RBAC bindings outside Alpacon")
				assert.Contains(t, stderr, bindingNotice)
			} else {
				assert.Contains(t, stderr, tt.hint)
				assert.NotContains(t, stderr, "cluster prod-east has RBAC bindings outside Alpacon")
				assert.NotContains(t, stderr, "external_grant")
			}
		})
	}
}

func TestCheckProxyURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		proxyURL  string
		proxyPort int
		plaintext bool
		wantErr   bool
	}{
		{name: "https", proxyURL: testProxyURL},
		{name: "https through the local proxy", proxyURL: testProxyURL, proxyPort: 18443},
		{name: "http", proxyURL: "http://ws.example.com/kube/c", plaintext: true},
		{name: "http through the local proxy", proxyURL: "http://ws.example.com/kube/c", proxyPort: 18443, plaintext: true, wantErr: true},
		{name: "empty", proxyURL: "", wantErr: true},
		{name: "relative", proxyURL: "/kube/c", wantErr: true},
		{name: "unparsable", proxyURL: "https://[::1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plaintext, err := checkProxyURL(tt.proxyURL, tt.proxyPort)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.plaintext, plaintext)
		})
	}
}

func TestValidatePort(t *testing.T) {
	t.Parallel()
	for _, port := range []int{1, 18443, 65535} {
		require.NoError(t, validatePort(port), "%d", port)
	}
	for _, port := range []int{-1, 0, 65536} {
		require.Error(t, validatePort(port), "%d", port)
	}
}

func TestKubeConfigResultJSONCarriesNoToken(t *testing.T) {
	t.Parallel()
	srv := newSessionServer()
	ac := srv.start(t)
	path := filepath.Join(t.TempDir(), "config")
	result, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{
		clusterName: "prod-east", workSessionID: "ws-1", kubeconfig: path,
	})
	require.NoError(t, err)

	rendered, err := utils.FormatJSON(result)
	require.NoError(t, err)

	assert.NotContains(t, rendered, testToken)
	assert.JSONEq(t, fmt.Sprintf(`{
		"context": "alpacon-prod-east",
		"kubeconfig": %q,
		"kube_session_id": %q,
		"cluster_id": "c-prod",
		"work_session_id": "ws-1",
		"proxy_url": %q,
		"token_expires_at": "2026-10-08T09:40:00Z",
		"previous_context": "",
		"rbac_drift_warning": null
	}`, path, testSessionID, testProxyURL), rendered)
}

// The warning reaches the result as the server sent it, so --output json shows
// the counts and the policy; without one the field is null, never missing.
func TestOpenKubeConfigCarriesTheRBACDriftWarning(t *testing.T) {
	t.Parallel()
	const warning = `{"cluster_external_grants":2,"namespaced_external_grants":1,"incomplete":false,"checked_at":"2026-11-10T09:00:00Z","policy":"warn"}`
	tests := []struct {
		name   string
		server string
		want   string
	}{
		{name: "older server", want: "null"},
		{name: "no warning", server: "null", want: "null"},
		{name: "warning", server: warning, want: warning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newSessionServer()
			srv.rbacDriftWarning = tt.server
			ac := srv.start(t)
			path := filepath.Join(t.TempDir(), "config")

			result, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
			require.NoError(t, err)
			assert.Equal(t, "prod-east", result.clusterName)

			rendered, err := utils.FormatJSON(result)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(rendered), &fields))
			require.Contains(t, fields, "rbac_drift_warning")
			assert.JSONEq(t, tt.want, string(fields["rbac_drift_warning"]))
		})
	}
}

// A Ctrl+C is honored at the next safe point: before the session is opened,
// nothing is opened; after, the session is closed instead of left behind.
func TestOpenKubeConfigStopsAtAnInterrupt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		afterCreate   bool
		expectCreates int
		expectCloses  int
	}{
		{name: "before the session is opened", expectCreates: 0, expectCloses: 0},
		{name: "after the session is opened", afterCreate: true, expectCreates: 1, expectCloses: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newSessionServer()
			ac := srv.start(t)
			path := filepath.Join(t.TempDir(), "config")
			interrupted := func() bool { return !tt.afterCreate || srv.creates() > 0 }

			_, err := openKubeConfig(ac, openHold(t, path), kubeConfigOptions{
				clusterName: "prod-east", kubeconfig: path, interrupted: interrupted,
			})

			require.ErrorIs(t, err, errInterrupted)
			assert.Equal(t, tt.expectCreates, srv.creates())
			assert.Equal(t, tt.expectCloses, srv.closes())
			assert.NoFileExists(t, path)
		})
	}
}

// lockProbe records whether the kubeconfig lock file existed when the result was written.
type lockProbe struct {
	lockPath   string
	wrote      bool
	lockedSeen bool
}

func (p *lockProbe) Write(b []byte) (int, error) {
	p.wrote = true
	if _, err := os.Stat(p.lockPath); err == nil {
		p.lockedSeen = true
	}
	return len(b), nil
}

func TestKubeConfigReleasesTheLockBeforePrintingTheResult(t *testing.T) {
	// Serial: it sets ALPACON_WORK_SESSION and utils.OutputFormat and swaps os.Stderr.
	t.Setenv("ALPACON_WORK_SESSION", "")
	withOutputFormat(t, utils.OutputFormatJSON) // the result goes to stdout only as JSON
	ac := newSessionServer().start(t)
	path := filepath.Join(t.TempDir(), "config")
	probe := &lockProbe{lockPath: path + ".lock"}
	ios, _, _ := utils.TestIOStreams()
	ios.Out = probe
	require.NoError(t, ios.SetOutputFormat(utils.OutputFormatJSON))
	cmd := NewKubeCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
	cmd.SetArgs([]string{"config", "prod-east", "--kubeconfig", path})

	var err error
	testutil.CaptureOutput(t, func() { err = cmd.Execute() })

	require.NoError(t, err)
	require.True(t, probe.wrote, "the result never reached stdout")
	assert.False(t, probe.lockedSeen, "a stdout write that kills the process would leave %s behind", probe.lockPath)
}

// A refusal the server names keeps its code and status in the failure, so the
// --output json envelope reports the server's error_code, and an expected
// refusal does not ask the user to report a CLI issue.
// Serial: it sets ALPACON_WORK_SESSION and utils.OutputFormat and swaps os.Stdout and os.Stderr.
func TestKubeConfigRefusalKeepsTheServerCode(t *testing.T) {
	t.Setenv("ALPACON_WORK_SESSION", "")
	for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(format, func(t *testing.T) {
			srv := newSessionServer()
			srv.createStatus = http.StatusBadRequest
			srv.createCode = utils.ClusterRBACDrift
			ac := srv.start(t)
			path := filepath.Join(t.TempDir(), "config")

			_, stderr, code := runKubeCommandWith(t, func() (*client.AlpaconClient, error) { return ac, nil }, format,
				"config", "prod-east", "--kubeconfig", path)

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.NotContains(t, stderr, "report on https://github.com")
			assert.NoFileExists(t, path)
			if format == utils.OutputFormatTable {
				assert.Contains(t, stderr, "Failed to open a kube session: cluster prod-east has RBAC bindings outside Alpacon")
				return
			}
			var envelope struct {
				ErrorCode  string `json:"error_code"`
				HTTPStatus int    `json:"http_status"`
			}
			require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stderr)), &envelope), "stderr: %s", stderr)
			assert.Equal(t, utils.ClusterRBACDrift, envelope.ErrorCode)
			assert.Equal(t, http.StatusBadRequest, envelope.HTTPStatus)
		})
	}
}

func TestKubeconfigHoldLetsGoAndTakesAgain(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	hold := openHold(t, path)
	held, err := hold.take(0)
	require.NoError(t, err)

	hold.letGo()
	assert.NoFileExists(t, path+".lock")

	editor, err := hold.take(0)
	require.NoError(t, err)
	assert.FileExists(t, path+".lock")
	assert.NotSame(t, held, editor, "the file is reread after the lock is let go")
	again, err := hold.take(0)
	require.NoError(t, err)
	assert.Same(t, editor, again)

	hold.close()
	hold.close()
	assert.NoFileExists(t, path+".lock")
}

// A second Ctrl+C closes the hold from the signal goroutine and exits. A take
// after it must not create a lock file the exiting process leaves behind.
func TestKubeconfigHoldRefusesToTakeAfterClose(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	hold := openHold(t, path)
	hold.letGo()
	hold.close()

	_, err := hold.take(0)
	require.ErrorIs(t, err, errHoldClosed)
	assert.NoFileExists(t, path+".lock")
}

func TestKubeconfigHoldTakeFailsOnAForeignLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	hold := openHold(t, path)
	hold.letGo()
	require.NoError(t, os.WriteFile(path+".lock", nil, 0o600))

	_, err := hold.take(0)
	require.ErrorIs(t, err, kubeconfig.ErrLocked)
	hold.close()
	assert.FileExists(t, path+".lock")
}
