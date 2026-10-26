package worksession

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateClusterSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		scopes  []string
		flags   clusterSessionFlags
		want    []string
		wantErr string
	}{
		{name: "kube", scopes: []string{"kube"}, want: []string{"kube"}},
		{name: "kube and kube_exec", scopes: []string{"kube_exec", "kube"}, want: []string{"kube", "kube_exec"}},
		{name: "kube_exec implies kube", scopes: []string{"kube_exec"}, want: []string{"kube", "kube_exec"}},
		{name: "no scope", scopes: nil, wantErr: "needs the kube scope"},
		{name: "a server scope", scopes: []string{"kube", "command"}, wantErr: "not command"},
		{name: "account", scopes: []string{"kube"}, flags: clusterSessionFlags{accountSet: true}, wantErr: "--account cannot be combined with --cluster"},
		{name: "sudo", scopes: []string{"kube", "sudo"}, flags: clusterSessionFlags{sudoSet: true}, wantErr: "--sudo cannot be combined with --cluster"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateClusterSession(tt.scopes, tt.flags)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// An agent session cannot hold kube_exec, the same way it cannot hold websh.
func TestValidateAgentScopesRefusesKubeExec(t *testing.T) {
	t.Parallel()
	require.Error(t, ValidateAgentScopes("agent", []string{"kube", "kube_exec"}))
	assert.NoError(t, ValidateAgentScopes("agent", []string{"kube"}))
	assert.NoError(t, ValidateAgentScopes("user", []string{"kube", "kube_exec"}))
}

func TestValidateServerScopes(t *testing.T) {
	t.Parallel()
	assert.NoError(t, validateServerScopes([]string{"command", "websh"}))
	assert.NoError(t, validateServerScopes(nil))
	for _, scope := range []string{"kube", "kube_exec"} {
		err := validateServerScopes([]string{"command", scope})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pass --cluster instead of --server")
	}
}

func TestTargetErrorHint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code string
		want string
	}{
		{utils.WorkSessionMixedTargets, "never both"},
		{utils.WorkSessionNoServers, "pass --server or --cluster"},
		{utils.WorkSessionTooManyClusters, "one cluster at most"},
		{utils.WorkSessionInvalidScope, "a cluster session holds kube"},
		{utils.ClusterNotFound, "'alpacon kube ls'"},
		{utils.WorkSessionTargetAccountNotApplicable, "takes no account"},
		{utils.WorkSessionExpiresExceedsMaxTTL, "shorter"},
		{utils.WorkSessionScopeNotAllowed, ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			t.Parallel()
			hint := TargetErrorHint(errors.New(`{"code":"` + tt.code + `"}`))
			if tt.want == "" {
				assert.Empty(t, hint)
				return
			}
			assert.Contains(t, hint, tt.want)
		})
	}
	assert.Empty(t, TargetErrorHint(errors.New("connection refused")))
}

// clusterCreateServer answers a cluster lookup and records the create body.
type clusterCreateServer struct {
	status   string
	clusters string // JSON array the cluster lookup answers; default prod-east
	notFound bool   // the cluster lookup answers 404, as with the surface off

	mu       sync.Mutex
	body     map[string]any
	requests int
}

func (s *clusterCreateServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/kubernetes/clusters/":
			if r.URL.Query().Get("name") != "prod-east" {
				t.Errorf("cluster lookup by %q", r.URL.Query().Get("name"))
			}
			if s.notFound {
				http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
				return
			}
			clusters := s.clusters
			if clusters == "" {
				clusters = `[{"id":"6a3f1c2e-0000-4000-8000-000000000001","name":"prod-east"}]`
			}
			_, _ = w.Write([]byte(`{"count":1,"results":` + clusters + `}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("create body: %v", err)
			}
			s.mu.Lock()
			s.body = body
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"ses-k","status":"` + s.status + `","approval_request_id":"apr-1","expires_at":"2026-06-01T12:00:00Z",
				"clusters":[{"id":"6a3f1c2e-0000-4000-8000-000000000001","name":"prod-east"}],"servers":[]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (s *clusterCreateServer) sent() (map[string]any, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body, s.requests
}

func TestWorkSessionCreateClusterSendsOnlyTheCluster(t *testing.T) {
	srv := &clusterCreateServer{status: "active"}
	ts := srv.start(t)
	setupWorkSessionCommandConfig(t, ts.URL)
	resetCreateCommandState(t)
	purpose = "debug api pods"
	createScopes = []string{"kube", "kube_exec"}
	createCluster = "prod-east"
	expiresIn = "1h"

	_, stderr := testutil.CaptureOutput(t, func() {
		workSessionCreateCmd.Run(workSessionCreateCmd, nil)
	})

	body, _ := srv.sent()
	require.NotNil(t, body)
	assert.Equal(t, []any{"6a3f1c2e-0000-4000-8000-000000000001"}, body["clusters"])
	assert.Equal(t, []any{"kube", "kube_exec"}, body["scopes"])
	assert.NotContains(t, body, "servers")
	assert.NotContains(t, body, "target_account_username")
	assert.NotContains(t, body, "sudo_policies")
	assert.Contains(t, stderr, "Next: alpacon kube login prod-east --work-session ses-k")
}

func TestWorkSessionCreateClusterPendingPointsAtKubeLogin(t *testing.T) {
	t.Parallel()
	srv := &clusterCreateServer{status: "pending"}
	ts := srv.start(t)

	stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), utils.OutputFormatJSON, ts.URL,
		"create", "--purpose", "debug", "--scope", "kube", "--cluster", "prod-east", "--expires-in", "1h")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stdout, `"command": "alpacon kube login prod-east --work-session ses-k"`)
	assert.NotContains(t, stdout, "work-session use")
}

func TestWorkSessionCreateClusterRefusesFlagsBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "account",
			args:    []string{"--scope", "kube", "--cluster", "prod-east", "--account", "root"},
			message: "--account cannot be combined with --cluster",
		},
		{
			name:    "sudo",
			args:    []string{"--scope", "kube", "--cluster", "prod-east", "--sudo", "systemctl restart nginx"},
			message: "--sudo cannot be combined with --cluster",
		},
		{
			name:    "server scope on a cluster",
			args:    []string{"--scope", "kube,command", "--cluster", "prod-east"},
			message: "a cluster session holds only kube and kube_exec",
		},
		{
			name:    "kube scope on servers",
			args:    []string{"--scope", "kube", "--server", "web-01"},
			message: "pass --cluster instead of --server",
		},
		{
			name:    "blank cluster",
			args:    []string{"--scope", "kube", "--cluster", " "},
			message: "--cluster must name a cluster",
		},
		{
			name:    "use",
			args:    []string{"--scope", "kube", "--cluster", "prod-east", "--use"},
			message: "--use cannot be combined with --cluster",
		},
		{
			name:    "agent with kube_exec",
			args:    []string{"--scope", "kube,kube_exec", "--cluster", "prod-east", "--requester-type", "agent"},
			message: `"kube_exec" is not allowed for agent sessions`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &clusterCreateServer{status: "active"}
			ts := srv.start(t)
			args := append([]string{"create", "--purpose", "debug", "--expires-in", "1h"}, tt.args...)

			_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), utils.OutputFormatTable, ts.URL, args...)

			assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.message)
			_, requests := srv.sent()
			assert.Zero(t, requests)
		})
	}
}

// Cobra refuses the pair itself, so it exits 1 like any other flag error.
func TestWorkSessionCreateRefusesServerWithCluster(t *testing.T) {
	t.Parallel()
	srv := &clusterCreateServer{status: "active"}
	ts := srv.start(t)

	_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), utils.OutputFormatTable, ts.URL,
		"create", "--purpose", "debug", "--expires-in", "1h", "--scope", "kube", "--server", "web-01", "--cluster", "prod-east")

	assert.NotZero(t, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "[cluster server]")
	_, requests := srv.sent()
	assert.Zero(t, requests)
}

func TestWorkSessionCreateClusterLookupFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		srv     *clusterCreateServer
		message string
	}{
		{name: "surface off", srv: &clusterCreateServer{notFound: true}, message: kubernetes.SurfaceNotEnabledMessage},
		{name: "unknown cluster", srv: &clusterCreateServer{clusters: "[]"}, message: "Failed to find the cluster: no cluster found with the given name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := tt.srv.start(t)

			_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), utils.OutputFormatTable, ts.URL,
				"create", "--purpose", "debug", "--expires-in", "1h", "--scope", "kube", "--cluster", "prod-east")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.message)
			body, requests := tt.srv.sent()
			assert.Nil(t, body, "no session is created")
			assert.Equal(t, 1, requests)
		})
	}
}

// update cannot turn a server session into a cluster one, so a kube scope
// sent with --server is refused before any request.
func TestWorkSessionUpdateRefusesAKubeScopeWithServers(t *testing.T) {
	t.Parallel()
	srv := &clusterCreateServer{status: "active"}
	ts := srv.start(t)

	_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), utils.OutputFormatTable, ts.URL,
		"update", "0b5d7e9a-0000-4000-8000-000000000002", "--scope", "kube", "--server", "web-01")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "pass --cluster instead of --server")
	_, requests := srv.sent()
	assert.Zero(t, requests)
}

func TestWorkSessionUseRefusesAClusterSessionAsAUsageError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/work-sessions/sessions/ses-k/" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ses-k","status":"active","requester_type":"user","scopes":["kube"],"clusters":[{"id":"c-1","name":"prod-east"}]}`))
	}))
	t.Cleanup(ts.Close)

	_, stderr, exitCode := runWorkSessionHelperWithEnv(t, agentHelperEnv(), utils.OutputFormatTable, ts.URL, "use", "ses-k")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "alpacon kube login prod-east --work-session ses-k")
}
