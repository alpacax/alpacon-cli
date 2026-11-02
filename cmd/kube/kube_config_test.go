package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/utils"
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
	proxyURL     string
	token        string
	closeStatus  int // 0 answers 200

	mu          sync.Mutex
	createBody  map[string]any
	createCalls int
	closeCalls  []string
}

func newSessionServer() *sessionServer {
	return &sessionServer{proxyURL: testProxyURL, token: testToken}
}

func (s *sessionServer) start(t *testing.T) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			s.mu.Unlock()
			if s.createStatus != 0 {
				w.WriteHeader(s.createStatus)
				_, _ = fmt.Fprintf(w, `{"code":%q}`, s.createCode)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":%q,"cluster":"c-prod","work_session":null,"user":null,
				"impersonate_user":"alice","impersonate_groups":[],"client_type":"cli",
				"token_expired_at":"2026-10-08T09:40:00Z","closed_at":null,"added_at":"2026-10-08T07:40:00Z",
				"updated_at":"2026-10-08T07:40:00Z","channel_id":"ch-1","channel_token":%q,"proxy_url":%q}`,
				testSessionID, s.token, s.proxyURL)
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
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
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

func openEditor(t *testing.T, path string) *kubeconfig.Editor {
	t.Helper()
	editor, err := kubeconfig.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = editor.Close() })
	return editor
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

			result, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{
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

	result, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
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

	_, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	var lookupErr *clusterLookupError
	require.ErrorAs(t, err, &lookupErr)
	assert.True(t, isSurfaceOff(lookupErr.err), "a 404 on the cluster list reads as the surface off: %v", err)
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

	_, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

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

			_, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{
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

	_, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

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
	editor := openEditor(t, path)
	// The directory turns read-only after the file was opened and checked, so
	// only the write itself fails.
	require.NoError(t, os.Chmod(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	_, err := openKubeConfig(ac, editor, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

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

	result, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

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
		{name: "agent work session", code: utils.KubeSessionAgentModeNotSupported, expected: "requested by an AI agent"},
		{name: "cluster not connected", code: utils.ClusterNotConnected, expected: "'alpacon kube describe prod-east'"},
		{name: "channel open failed", code: utils.KubeSessionChannelOpenFailed, expected: "try again"},
		{name: "no user name", code: utils.KubeSessionPrincipalNotSupported, expected: "log in as a user"},
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

			_, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
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
	hinted := hintedKubeError{code: utils.KubeSessionAgentModeNotSupported, hint: "Open the kube session from a work session you created."}
	assert.Empty(t, kubeSessionErrorHint(hinted, "prod-east"))

	plain := hintedKubeError{code: utils.KubeSessionAgentModeNotSupported}
	assert.Contains(t, kubeSessionErrorHint(plain, "prod-east"), "requested by an AI agent")
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
	result, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{
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
		"previous_context": ""
	}`, path, testSessionID, testProxyURL), rendered)
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

			_, err := openKubeConfig(ac, openEditor(t, path), kubeConfigOptions{
				clusterName: "prod-east", kubeconfig: path, interrupted: interrupted,
			})

			require.ErrorIs(t, err, errInterrupted)
			assert.Equal(t, tt.expectCreates, srv.creates())
			assert.Equal(t, tt.expectCloses, srv.closes())
			assert.NoFileExists(t, path)
		})
	}
}
