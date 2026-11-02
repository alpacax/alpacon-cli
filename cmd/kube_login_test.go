package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const (
	loginClusterID = "6a3f1c2e-0000-4000-8000-000000000001"
	loginWSID      = "0b5d7e9a-0000-4000-8000-000000000002"
	serverWSID     = "0b5d7e9a-0000-4000-8000-0000000000f0" // a server session
	loginToken     = "login-channel-token"
)

// kubeLoginServer answers the requests 'kube login' makes. Its fields set
// how the work session and the kube session come out.
type kubeLoginServer struct {
	wsCreateStatus string // status of a created work session
	wsPollStatus   string // status a read of it returns
	wsList         string // JSON array of the caller's sessions for the cluster
	kubeCreateCode string // code refusing the kube session, if any
	pendingReads   int    // reads of the session that answer pending before wsPollStatus
	kubeClosed     bool   // a read of the kube session answers it closed
	kubeReadRefuse string // body of a 403 a read of the kube session answers, if any
	requesterType  string // requester_type the session list must filter on; default user
	clusters       string // JSON array the cluster lookup answers; default prod-east
	surfaceOff     bool   // the cluster lookup answers 404

	mu       sync.Mutex
	requests []string
	wsBody   map[string]any
	kubeBody map[string]any
	closed   []string
}

func (s *kubeLoginServer) workSession(status string) string {
	return fmt.Sprintf(`{"id":%q,"status":%q,"requester_type":"user","scopes":["kube"],
		"clusters":[{"id":%q,"name":"prod-east"}],"servers":[],"approval_request_id":"apr-1",
		"expires_at":"2099-01-01T00:00:00Z"}`, loginWSID, status, loginClusterID)
}

func (s *kubeLoginServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		decode := func() map[string]any {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("request body of %s: %v", r.URL.Path, err)
			}
			return body
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/kubernetes/clusters/":
			if s.surfaceOff {
				http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
				return
			}
			clusters := s.clusters
			if clusters == "" {
				clusters = fmt.Sprintf(`[{"id":%q,"name":"prod-east"}]`, loginClusterID)
			}
			_, _ = fmt.Fprintf(w, `{"count":1,"results":%s}`, clusters)
		case r.Method == http.MethodGet && r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"id":"u-1","username":"alice"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/":
			if got := r.URL.Query().Get("cluster"); got != loginClusterID {
				t.Errorf("session list filtered by cluster %q", got)
			}
			if got := r.URL.Query().Get("status"); got != "active,approved,pending" {
				t.Errorf("session list filtered by status %q", got)
			}
			// Only the caller's own sessions may be reused or waited on.
			if got := r.URL.Query().Get("assigned_user"); got != "u-1" {
				t.Errorf("session list filtered by assigned_user %q", got)
			}
			wantRequester := s.requesterType
			if wantRequester == "" {
				wantRequester = "user"
			}
			if got := r.URL.Query().Get("requester_type"); got != wantRequester {
				t.Errorf("session list filtered by requester_type %q, want %q", got, wantRequester)
			}
			list := s.wsList
			if list == "" {
				list = "[]"
			}
			_, _ = fmt.Fprintf(w, `{"count":0,"results":%s}`, list)
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			body := decode()
			s.mu.Lock()
			s.wsBody = body
			s.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(s.workSession(s.wsCreateStatus)))
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/"+serverWSID+"/":
			_, _ = fmt.Fprintf(w, `{"id":%q,"status":"active","requester_type":"user","scopes":["command"],
				"servers":[{"id":"s-1","name":"web-01"}],"clusters":[],"expires_at":"2099-01-01T00:00:00Z"}`, serverWSID)
		case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/"+loginWSID+"/":
			s.mu.Lock()
			status := s.wsPollStatus
			if s.pendingReads > 0 {
				s.pendingReads--
				status = "pending"
			}
			s.mu.Unlock()
			_, _ = w.Write([]byte(s.workSession(status)))
		case r.Method == http.MethodPost && r.URL.Path == "/api/kubernetes/sessions/":
			body := decode()
			s.mu.Lock()
			s.kubeBody = body
			s.mu.Unlock()
			if s.kubeCreateCode != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w, `{"code":%q}`, s.kubeCreateCode)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":"7c1e0000-0000-4000-8000-000000000003","cluster":%q,"channel_token":%q,
				"token_expired_at":"2099-01-01T00:00:00Z","proxy_url":"https://ws.example.com/kube/%s"}`, loginClusterID, loginToken, loginClusterID)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/kubernetes/sessions/"):
			closedAt := "null"
			s.mu.Lock()
			closed, refuse := s.kubeClosed, s.kubeReadRefuse
			s.mu.Unlock()
			if refuse != "" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(refuse))
				return
			}
			if closed {
				closedAt = `"2026-10-22T09:00:00Z"`
			}
			_, _ = fmt.Fprintf(w, `{"id":"7c1e0000-0000-4000-8000-000000000003","token_expired_at":"2099-01-02T00:00:00Z","closed_at":%s}`, closedAt)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/kubernetes/sessions/") && strings.HasSuffix(r.URL.Path, "/close/"):
			s.mu.Lock()
			s.closed = append(s.closed, r.URL.Path)
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (s *kubeLoginServer) bodies() (workSession, kubeSession map[string]any, closed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wsBody, s.kubeBody, append([]string(nil), s.closed...)
}

func (s *kubeLoginServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// kubeLoginHome is a home logged in through the browser, with the surface on.
// extra adds config keys, such as an active work session.
func kubeLoginHome(t *testing.T, workspaceURL string, extra ...map[string]any) string {
	t.Helper()
	cfg := map[string]any{
		"workspace_url": workspaceURL, "workspace_name": "ws", "kubernetes_surface": true,
		"access_token": "jwt", "access_token_expires_at": "2099-01-01T00:00:00Z",
	}
	for _, e := range extra {
		maps.Copy(cfg, e)
	}
	return writeKubeGateConfig(t, cfg)
}

// writeKubeRecord leaves a saved kube session for prod-east of the workspace
// at workspaceURL under home, and returns its path.
func writeKubeRecord(t *testing.T, home, workspaceURL string, record map[string]any) string {
	t.Helper()
	dir := filepath.Join(home, ".alpacon", "kube")
	require.NoError(t, os.MkdirAll(dir, 0700))
	sum := sha256.Sum256([]byte(workspaceURL + "\n" + "prod-east"))
	data, err := json.Marshal(record)
	require.NoError(t, err)
	path := filepath.Join(dir, hex.EncodeToString(sum[:])[:16]+".json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

func (s *kubeLoginServer) count(request string) int {
	n := 0
	for _, r := range s.seen() {
		if r == request {
			n++
		}
	}
	return n
}

// runKubeLogin runs the command in a child with no AI agent in its
// environment, so the session it asks for is a person's.
func runKubeLogin(t *testing.T, home string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runKubeLoginWithEnv(t, home, nil, args...)
}

// runKubeLoginWithEnv appends extra ("KEY=value") last, so it overrides.
func runKubeLoginWithEnv(t *testing.T, home string, extra []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	env := []string{"GO_WANT_KUBE_GATE_HELPER=1", homeEnvVar() + "=" + home, "ALPACON_WORK_SESSION="}
	for _, name := range aiagent.Variables() {
		env = append(env, name+"=")
	}
	env = append(env, extra...)
	return runHelperProcess(t, "TestKubeGateHelperProcess", "kube-gate-helper", append([]string{"kube", "login"}, args...), env...)
}

func TestKubeLoginCreatesAClusterSessionAndWritesTheExecContext(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")

	stdout, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", path,
		"--purpose", "debug api pods", "--scope", "kube_exec", "--duration", "90m")

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, `Context "alpacon-prod-east" ready (session expires `)
	assert.NotContains(t, stdout+stderr, loginToken)

	wsBody, kubeBody, _ := srv.bodies()
	assert.Equal(t, map[string]any{
		"description":    "debug api pods",
		"requester_type": "user",
		"scopes":         []any{"kube", "kube_exec"},
		"clusters":       []any{loginClusterID},
		"duration":       "01:30:00",
	}, wsBody, "no servers, no account and no absolute expiry")
	assert.Equal(t, loginWSID, kubeBody["work_session"])

	var cfg struct {
		Users []struct {
			Name string         `yaml:"name"`
			User map[string]any `yaml:"user"`
		} `yaml:"users"`
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	require.Len(t, cfg.Users, 1)
	assert.Equal(t, map[string]any{"exec": map[string]any{
		"apiVersion":         "client.authentication.k8s.io/v1",
		"command":            "alpacon",
		"args":               []any{"kube", "credentials", "--cluster", "prod-east"},
		"interactiveMode":    "Never",
		"provideClusterInfo": true,
	}}, cfg.Users[0].User)
	assert.NotContains(t, string(data), loginToken)

	records, err := filepath.Glob(filepath.Join(home, ".alpacon", "kube", "*.json"))
	require.NoError(t, err)
	require.Len(t, records, 1)
	var record map[string]any
	raw, err := os.ReadFile(records[0])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &record))
	assert.Equal(t, "prod-east", record["cluster"])
	assert.Equal(t, loginToken, record["channel_token"])
	assert.Equal(t, loginWSID, record["work_session_id"])
	assert.Equal(t, "7c1e0000-0000-4000-8000-000000000003", record["kube_session_id"])
	if runtime.GOOS != "windows" {
		info, err := os.Stat(records[0])
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
}

// A second login reuses the session the first one used, opens a new kube
// session, and closes the one the first login opened.
func TestKubeLoginAgainReusesTheSessionAndClosesThePreviousKubeSession(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active", wsPollStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")
	args := []string{"prod-east", "--kubeconfig", path, "--purpose", "x", "--scope", "kube", "--duration", "1h"}
	_, stderr, exitCode := runKubeLogin(t, home, args...)
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)

	// The kube session id comes back the same, so the record names another.
	records, err := filepath.Glob(filepath.Join(home, ".alpacon", "kube", "*.json"))
	require.NoError(t, err)
	require.Len(t, records, 1)
	raw, err := os.ReadFile(records[0])
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(records[0], []byte(strings.Replace(string(raw),
		"7c1e0000-0000-4000-8000-000000000003", "7c1e0000-0000-4000-8000-0000000000aa", 1)), 0600))

	_, stderr, exitCode = runKubeLogin(t, home, "prod-east", "--kubeconfig", path)
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "Using WorkSession "+loginWSID)
	creates := 0
	for _, r := range srv.seen() {
		if r == "POST /api/work-sessions/sessions/" {
			creates++
		}
	}
	assert.Equal(t, 1, creates, "the second login creates no work session")
	_, _, closed := srv.bodies()
	assert.Equal(t, []string{"/api/kubernetes/sessions/7c1e0000-0000-4000-8000-0000000000aa/close/"}, closed)
}

func TestKubeLoginRefusesBadInputBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{name: "unknown scope", args: []string{"--scope", "kube,exec"}, message: "holds only kube and kube_exec, not exec"},
		{name: "bad duration", args: []string{"--duration", "soon"}, message: "invalid --duration value"},
		{name: "zero wait", args: []string{"--wait-approval", "0s"}, message: "invalid --wait-approval value"},
		{name: "bad port", args: []string{"--proxy-port", "70000"}, message: "--proxy-port must be a port number"},
		{name: "session and purpose", args: []string{"--work-session", loginWSID, "--purpose", "x"}, message: "cannot be combined with --work-session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &kubeLoginServer{}
			ts := srv.start(t)
			home := kubeLoginHome(t, ts.URL)

			_, stderr, exitCode := runKubeLogin(t, home, append([]string{"prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config")}, tt.args...)...)

			assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.message)
			assert.Empty(t, srv.seen())
		})
	}
}

func TestKubeLoginWithoutPurposeNonInteractivelyIsAUsageError(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"))

	assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "Non-interactive mode requires --purpose, --scope and --duration")
	assert.NotContains(t, srv.seen(), "POST /api/work-sessions/sessions/")
}

func TestKubeLoginRefusesATokenLogin(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{}
	ts := srv.start(t)
	home := kubeEnabledHome(t, ts.URL)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
		"--purpose", "x", "--scope", "kube", "--duration", "1h")

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Contains(t, stderr, "cannot open a kube session")
	assert.Empty(t, srv.seen(), "a token never creates a work session it cannot use")
}

func TestKubeLoginApprovalOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		poll     string
		exitCode int
		message  string
	}{
		{name: "still pending", poll: "pending", exitCode: utils.ExitCodePendingApproval, message: "alpacon kube login prod-east --work-session " + loginWSID},
		{name: "rejected", poll: "rejected", exitCode: utils.ExitCodeNotApproved, message: "work session was rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := &kubeLoginServer{wsCreateStatus: "pending", wsPollStatus: tt.poll}
			ts := srv.start(t)
			home := kubeLoginHome(t, ts.URL)
			path := filepath.Join(t.TempDir(), "config")

			_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", path,
				"--purpose", "x", "--scope", "kube", "--duration", "1h", "--wait-approval", "1s")

			assert.Equal(t, tt.exitCode, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.message)
			assert.NotContains(t, srv.seen(), "POST /api/kubernetes/sessions/")
			assert.NoFileExists(t, path)
		})
	}
}

// A login cut short while its session waited on approval comes back to that
// session instead of filing a second one.
func TestKubeLoginResumesAPendingSessionForTheCluster(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsPollStatus: "active"}
	srv.wsList = "[" + srv.workSession("pending") + "]"
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"))

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.NotContains(t, srv.seen(), "POST /api/work-sessions/sessions/")
	_, kubeBody, _ := srv.bodies()
	assert.Equal(t, loginWSID, kubeBody["work_session"])
}

func TestKubeLoginGateRefusalExitsWithTheGateCode(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{kubeCreateCode: utils.WorkSessionClusterNotAllowed}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", path, "--work-session", loginWSID)

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "target cluster is not in this session")
	assert.Contains(t, stderr, "target cluster: prod-east")
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, path+".lock")
	records, err := filepath.Glob(filepath.Join(home, ".alpacon", "kube", "*.json"))
	require.NoError(t, err)
	assert.Empty(t, records)
}

// The command the wait prints to resume names the session, so naming a
// session still pending waits on it rather than failing at the gate.
func TestKubeLoginWaitsOnANamedPendingSession(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsPollStatus: "active", pendingReads: 1}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"), "--work-session", loginWSID)

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "is pending; waiting up to 5m0s")
	_, kubeBody, _ := srv.bodies()
	assert.Equal(t, loginWSID, kubeBody["work_session"])
}

// An agent session holds only kube, so an agent need not say so.
func TestKubeLoginGivesAnAgentTheKubeScope(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active", requesterType: "agent"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	_, stderr, exitCode := runKubeLoginWithEnv(t, home, []string{"AI_AGENT=test-agent"},
		"prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"), "--purpose", "x", "--duration", "1h")

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	wsBody, _, _ := srv.bodies()
	assert.Equal(t, "agent", wsBody["requester_type"])
	assert.Equal(t, []any{"kube"}, wsBody["scopes"])
}

func TestKubeLoginFailuresAreEnvelopesUnderJSON(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{}
	ts := srv.start(t)
	home := kubeEnabledHome(t, ts.URL)

	stdout, stderr, exitCode := runKubeGate(t, home, "--output", "json", "kube", "login", "prod-east",
		"--kubeconfig", filepath.Join(t.TempDir(), "config"))

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
	assert.Empty(t, stdout)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
	assert.Equal(t, false, envelope["ok"])
	assert.InDelta(t, utils.ExitCodeGeneralError, envelope["exit_code"], 0)
	assert.Contains(t, envelope["message"], "cannot open a kube session")
}

// The active slot holds only a server session, which no kube session opens
// under, so it is not read: a fitting session comes from the list or is created.
func TestKubeLoginIgnoresTheActiveWorkSession(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL, map[string]any{"active_work_sessions": map[string]string{"ws": serverWSID}})

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
		"--purpose", "x", "--scope", "kube", "--duration", "1h")

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Zero(t, srv.count("GET /api/work-sessions/sessions/"+serverWSID+"/"))
	assert.Equal(t, 1, srv.count("POST /api/work-sessions/sessions/"))
}

// ALPACON_WORK_SESSION is used when it serves the cluster, waited on while it
// is pending, and passed over for a lookup when it names a server session.
func TestKubeLoginWithTheWorkSessionVariable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		srv     *kubeLoginServer
		env     string
		args    []string
		message string
		creates int
	}{
		{name: "usable", srv: &kubeLoginServer{wsPollStatus: "active"}, env: loginWSID,
			message: "Using WorkSession " + loginWSID + " from ALPACON_WORK_SESSION"},
		{name: "pending", srv: &kubeLoginServer{wsPollStatus: "active", pendingReads: 1}, env: loginWSID,
			message: "is pending; waiting up to 5m0s"},
		{name: "a server session", srv: &kubeLoginServer{wsCreateStatus: "active"}, env: serverWSID,
			args:    []string{"--purpose", "x", "--scope", "kube", "--duration", "1h"},
			message: "ALPACON_WORK_SESSION names a WorkSession that does not serve prod-east", creates: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := tt.srv.start(t)
			home := kubeLoginHome(t, ts.URL)

			_, stderr, exitCode := runKubeLoginWithEnv(t, home, []string{"ALPACON_WORK_SESSION=" + tt.env},
				append([]string{"prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config")}, tt.args...)...)

			require.Equal(t, 0, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.message)
			assert.Equal(t, tt.creates, tt.srv.count("POST /api/work-sessions/sessions/"))
			_, kubeBody, _ := tt.srv.bodies()
			assert.Equal(t, loginWSID, kubeBody["work_session"])
		})
	}
}

func TestKubeLoginClusterLookupFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		srv     *kubeLoginServer
		message string
	}{
		{name: "surface off", srv: &kubeLoginServer{surfaceOff: true}, message: "Kubernetes support is not enabled on this workspace"},
		{name: "unknown cluster", srv: &kubeLoginServer{clusters: "[]"}, message: "Failed to find the cluster: no cluster found with the given name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := tt.srv.start(t)
			home := kubeLoginHome(t, ts.URL)

			_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
				"--purpose", "x", "--scope", "kube", "--duration", "1h")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, tt.message)
			assert.Equal(t, []string{"GET /api/kubernetes/clusters/"}, tt.srv.seen(), "no WorkSession is looked up or created")
		})
	}
}

// A --work-session that is not one path segment never reaches a request path.
func TestKubeLoginRefusesAnUnusableWorkSessionID(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"), "--work-session", "../x")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "Invalid --work-session")
	assert.Equal(t, []string{"GET /api/kubernetes/clusters/"}, srv.seen())
}

func TestKubeLoginPicksAnActiveSessionFromTheList(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{}
	srv.wsList = "[" + srv.workSession("active") + "]"
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"))

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Zero(t, srv.count("POST /api/work-sessions/sessions/"))
	assert.Zero(t, srv.count("GET /api/work-sessions/sessions/"+loginWSID+"/"), "an active session from the list is not waited on")
}

// A record another workspace left is neither reused nor closed: this login's
// credentials do not reach it.
func TestKubeLoginLeavesAnotherWorkspacesRecordAlone(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	other := writeKubeRecord(t, home, "https://other.example.com", map[string]any{
		"cluster": "prod-east", "workspace_name": "other", "workspace_url": "https://other.example.com",
		"kube_session_id": "7c1e0000-0000-4000-8000-0000000000bb", "work_session_id": "0b5d7e9a-0000-4000-8000-0000000000bb",
		"channel_token": "other-token",
	})
	before, err := os.ReadFile(other)
	require.NoError(t, err)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
		"--purpose", "x", "--scope", "kube", "--duration", "1h")

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Zero(t, srv.count("GET /api/work-sessions/sessions/0b5d7e9a-0000-4000-8000-0000000000bb/"))
	_, _, closed := srv.bodies()
	assert.Empty(t, closed)
	after, err := os.ReadFile(other)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the other workspace keeps its token")
	records, err := filepath.Glob(filepath.Join(home, ".alpacon", "kube", "*.json"))
	require.NoError(t, err)
	assert.Len(t, records, 2)
}

func TestKubeLoginJSONResult(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")

	stdout, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--output", "json",
		"--kubeconfig", path, "--purpose", "x", "--scope", "kube", "--duration", "1h")

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.JSONEq(t, fmt.Sprintf(`{
		"context": "alpacon-prod-east",
		"kubeconfig": %q,
		"kube_session_id": "7c1e0000-0000-4000-8000-000000000003",
		"cluster_id": %q,
		"work_session_id": %q,
		"proxy_url": "https://ws.example.com/kube/%s",
		"token_expires_at": "2099-01-01T00:00:00Z",
		"previous_context": "",
		"work_session_created": true,
		"scopes": ["kube"]
	}`, path, loginClusterID, loginWSID, loginClusterID), stdout)
}

func TestKubeLoginArgumentCountIsAUsageError(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{}, {"prod-east", "prod-west"}} {
		t.Run(fmt.Sprintf("%d_arguments", len(args)), func(t *testing.T) {
			t.Parallel()
			srv := &kubeLoginServer{}
			ts := srv.start(t)
			home := kubeLoginHome(t, ts.URL)

			stdout, stderr, exitCode := runKubeLogin(t, home, append(args, "--output", "json")...)

			assert.Equal(t, utils.ExitCodeUsageError, exitCode, "stderr: %s", stderr)
			assert.Empty(t, stdout)
			var envelope map[string]any
			require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
			assert.Equal(t, "usage_error", envelope["error_code"])
			assert.Empty(t, srv.seen())
		})
	}
}

// The command printed to come back to a pending session writes the same file
// through the same local proxy.
func TestKubeLoginResumeCommandKeepsItsFlags(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "pending", wsPollStatus: "pending"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", path, "--proxy-port", "18443",
		"--purpose", "x", "--scope", "kube", "--duration", "1h", "--wait-approval", "1s")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "stderr: %s", stderr)
	// How the path is quoted follows the platform's shell; a Windows temp path
	// carries a ~ and gets double quotes.
	assert.Contains(t, stderr, "--work-session "+loginWSID+" --kubeconfig ")
	assert.Contains(t, stderr, path)
	assert.Contains(t, stderr, " --proxy-port 18443")
}

// runKubeCredentials runs the plugin the way kubectl does, with the cluster
// it is about to call in KUBERNETES_EXEC_INFO.
func runKubeCredentials(t *testing.T, home string) (stdout, stderr string, exitCode int) {
	t.Helper()
	info := fmt.Sprintf(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"cluster":{"server":"https://ws.example.com/kube/%s"},"interactive":false}}`, loginClusterID)
	return runHelperProcess(t, "TestKubeGateHelperProcess", "kube-gate-helper",
		[]string{"kube", "credentials", "--cluster", "prod-east"},
		"GO_WANT_KUBE_GATE_HELPER=1", homeEnvVar()+"="+home, "KUBERNETES_EXEC_INFO="+info)
}

// The context kube login writes runs kube credentials, which hands kubectl
// the saved token with the expiry the server reports now.
func TestKubeCredentialsServesTheLoginToken(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
		"--purpose", "x", "--scope", "kube", "--duration", "1h")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)

	stdout, stderr, exitCode := runKubeCredentials(t, home)

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.JSONEq(t, `{
		"apiVersion": "client.authentication.k8s.io/v1",
		"kind": "ExecCredential",
		"status": {"token": "`+loginToken+`", "expirationTimestamp": "2099-01-02T00:00:00Z"}
	}`, stdout)
	assert.Contains(t, srv.seen(), "GET /api/kubernetes/sessions/7c1e0000-0000-4000-8000-000000000003/")
}

func TestKubeCredentialsEndsWithTheGateCodeForAClosedSession(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
		"--purpose", "x", "--scope", "kube", "--duration", "1h")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	srv.mu.Lock()
	srv.kubeClosed = true
	srv.mu.Unlock()

	stdout, stderr, exitCode := runKubeCredentials(t, home)

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitCode, "stderr: %s", stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "run 'alpacon kube login prod-east'")
	assert.NotContains(t, stderr, loginToken)
}

// A check the server refuses keeps the token back, but a new kube login would
// meet the same refusal, so the message names it along with the server's hint.
func TestKubeCredentialsNamesARefusedCheck(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
		"--purpose", "x", "--scope", "kube", "--duration", "1h")
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	srv.mu.Lock()
	srv.kubeReadRefuse = `{"code":"auth_ip_not_allowed","detail":"Requests from this address are not allowed.","hint":"Connect from an allowed network."}`
	srv.mu.Unlock()

	stdout, stderr, exitCode := runKubeCredentials(t, home)

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitCode, "stderr: %s", stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "The server refused to check the kube session for prod-east, so the saved token is not used")
	assert.Contains(t, stderr, "Connect from an allowed network.")
	assert.NotContains(t, stderr, "has ended")
	assert.NotContains(t, stderr, loginToken)
}

func TestKubeCredentialsWithoutALoginEndsWithTheGateCode(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	stdout, stderr, exitCode := runKubeCredentials(t, home)

	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitCode, "stderr: %s", stderr)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "No kube session is saved for prod-east")
}

// --intent was the flag's first name and still reaches --purpose.
func TestKubeLoginTakesIntentAsPurpose(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", filepath.Join(t.TempDir(), "config"),
		"--intent", "debug api pods", "--scope", "kube", "--duration", "1h")

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	wsBody, _, _ := srv.bodies()
	assert.Equal(t, "debug api pods", wsBody["description"])
}

// A kubeconfig kube login cannot write stops it before a WorkSession, and so
// an approval request, exists.
func TestKubeLoginChecksTheKubeconfigBeforeAnyWorkSession(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsCreateStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte("clusters: [unclosed\n"), 0600))

	_, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--kubeconfig", path,
		"--purpose", "x", "--scope", "kube", "--duration", "1h")

	assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "Failed to open the kubeconfig")
	assert.Empty(t, srv.seen(), "nothing is asked of the server for a login that cannot be written")
}

// A named session is read before it is used, so the result reports the
// scopes it grants like any other.
func TestKubeLoginJSONResultForANamedSession(t *testing.T) {
	t.Parallel()
	srv := &kubeLoginServer{wsPollStatus: "active"}
	ts := srv.start(t)
	home := kubeLoginHome(t, ts.URL)

	stdout, stderr, exitCode := runKubeLogin(t, home, "prod-east", "--output", "json",
		"--kubeconfig", filepath.Join(t.TempDir(), "config"), "--work-session", loginWSID)

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, loginWSID, result["work_session_id"])
	assert.Equal(t, false, result["work_session_created"])
	assert.Equal(t, []any{"kube"}, result["scopes"])
}
