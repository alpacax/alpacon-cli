package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const kubeConfigTestToken = "channel-token-value"

type kubeSessionServer struct {
	createCode string
	lookups    atomic.Int32
	creates    atomic.Int32
}

func (s *kubeSessionServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/kubernetes/clusters/":
			s.lookups.Add(1)
			_, _ = w.Write([]byte(`{"count":1,"current":1,"next":null,"previous":null,"last":1,"results":[{"id":"c-prod","name":"prod-east"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/kubernetes/sessions/":
			s.creates.Add(1)
			if s.createCode != "" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = fmt.Fprintf(w, `{"code":%q}`, s.createCode)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":"s-1","cluster":"c-prod","channel_id":"ch-1","channel_token":%q,
				"token_expired_at":"2026-10-08T09:40:00Z","proxy_url":"https://ws.example.com/kube/c-prod"}`, kubeConfigTestToken)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func kubeEnabledHome(t *testing.T, workspaceURL string) string {
	t.Helper()
	return writeKubeGateConfig(t, map[string]any{
		"workspace_url": workspaceURL, "workspace_name": "ws", "token": "tok", "kubernetes_surface": true,
	})
}

func TestKubeConfigWritesTheContext(t *testing.T) {
	t.Parallel()
	srv := &kubeSessionServer{}
	ts := srv.start(t)
	home := kubeEnabledHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "kube", "config")

	stdout, stderr, exitCode := runKubeGate(t, home, "--output", "json", "kube", "config", "prod-east",
		"--work-session", "ws-1", "--kubeconfig", path)

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, "alpacon-prod-east", result["context"])
	assert.Equal(t, "s-1", result["kube_session_id"])
	assert.NotContains(t, stdout+stderr, kubeConfigTestToken)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "current-context: alpacon-prod-east")
	assert.Contains(t, string(data), "token: "+kubeConfigTestToken)
	assert.NoFileExists(t, path+".lock")
}

func TestKubeConfigWithoutWorkSessionExitsWithTheGateCode(t *testing.T) {
	t.Parallel()
	srv := &kubeSessionServer{createCode: utils.WorkSessionRequired}
	ts := srv.start(t)
	home := kubeEnabledHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")

	_, stderr, exitCode := runKubeGate(t, home, "kube", "config", "prod-east", "--kubeconfig", path)

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "opening a kube session requires a WorkSession in the active state that targets the cluster")
	assert.Contains(t, stderr, "target cluster: prod-east")
	assert.Contains(t, stderr, "alpacon kube login prod-east")
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, path+".lock", "the kubeconfig lock is released on the way out")
}

// The token is handed out once, so a kubeconfig that cannot be written must
// stop the command before a session is opened for it.
func TestKubeConfigOpensNoSessionForAnUnwritableKubeconfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare func(t *testing.T, path string)
		message string
	}{
		{name: "malformed", prepare: func(t *testing.T, path string) {
			t.Helper()
			require.NoError(t, os.WriteFile(path, []byte("clusters: not-a-list\n"), 0600))
		}, message: "Failed to open the kubeconfig"},
		{name: "locked by another writer", prepare: func(t *testing.T, path string) {
			t.Helper()
			require.NoError(t, os.WriteFile(path+".lock", nil, 0600))
		}, message: "config.lock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &kubeSessionServer{}
			ts := srv.start(t)
			home := kubeEnabledHome(t, ts.URL)
			path := filepath.Join(t.TempDir(), "config")
			tt.prepare(t, path)
			before, _ := os.ReadFile(path)

			_, stderr, exitCode := runKubeGate(t, home, "kube", "config", "prod-east", "--work-session", "ws-1", "--kubeconfig", path)

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.message)
			assert.Zero(t, srv.creates.Load(), "no session may be opened for a file that cannot take it")
			after, _ := os.ReadFile(path)
			assert.Equal(t, before, after)
		})
	}
}

func TestKubeConfigRejectsAnInvalidProxyPort(t *testing.T) {
	t.Parallel()
	srv := &kubeSessionServer{}
	ts := srv.start(t)
	home := kubeEnabledHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")

	_, stderr, exitCode := runKubeGate(t, home, "kube", "config", "prod-east", "--kubeconfig", path, "--proxy-port", "0")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Contains(t, stderr, "--proxy-port must be a port number")
	assert.Zero(t, srv.lookups.Load())
	assert.NoFileExists(t, path)
}

func TestKubeProxyRefusesASecondProxyOnTheSamePort(t *testing.T) {
	t.Parallel()
	home := kubeEnabledHome(t, "https://ws.example.com")
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte(`apiVersion: v1
kind: Config
clusters:
- name: alpacon-prod-east
  cluster:
    server: https://ws.example.com/kube/c-prod
    proxy-url: http://127.0.0.1:18443
users:
- name: alpacon-prod-east
  user:
    token: t
contexts:
- name: alpacon-prod-east
  context:
    cluster: alpacon-prod-east
    user: alpacon-prod-east
current-context: alpacon-prod-east
`), 0600))
	// The lock a running 'kube proxy prod-east --port 18443' would hold.
	sum := sha256.Sum256([]byte("prod-east"))
	lockPath := filepath.Join(home, ".alpacon", ".kube_proxy_lock."+hex.EncodeToString(sum[:])[:16]+"."+strconv.Itoa(18443))
	lock, err := filelock.Acquire(lockPath, 0644)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })

	_, stderr, exitCode := runKubeGate(t, home, "kube", "proxy", "prod-east", "--kubeconfig", path)

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Contains(t, stderr, "A kube proxy for prod-east on port 18443 is already running")
}

func TestKubeProxyNeedsTheContextFirst(t *testing.T) {
	t.Parallel()
	home := kubeEnabledHome(t, "https://ws.example.com")
	path := filepath.Join(t.TempDir(), "config")

	_, stderr, exitCode := runKubeGate(t, home, "kube", "proxy", "prod-east", "--kubeconfig", path)

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Contains(t, stderr, "Run 'alpacon kube config prod-east' first")
}
