package kube

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file stay serial: they swap os.Stdout through
// CaptureOutput, flip utils.OutputFormat, and read the package-level KubeCmd.

const (
	prodCluster = `{"id":"c-prod","name":"prod-east","provider":"eks","kube_version":"v1.31.2","agent_version":"0.1.0",
		"node_count":3,"status":null,"enabled":true,"groups":["g-1","g-2"],"tags":{"env":"prod","team":"infra"},
		"is_connected":true,"last_connectivity":"2026-09-30T08:15:42Z","last_synced_at":null,
		"added_at":"2026-09-18T01:02:03Z","updated_at":"2026-09-30T00:00:00Z"}`
	freshCluster = `{"id":"c-new","name":"fresh","provider":"unknown","kube_version":null,"agent_version":"",
		"node_count":null,"status":null,"enabled":true,"groups":[],"tags":{},
		"is_connected":false,"last_connectivity":null,"last_synced_at":null,
		"added_at":"2026-09-18T01:02:03Z","updated_at":"2026-09-30T00:00:00Z"}`
)

type namedCluster struct {
	name string
	body string
}

var (
	prod  = namedCluster{name: "prod-east", body: prodCluster}
	fresh = namedCluster{name: "fresh", body: freshCluster}
)

type fakeServer struct {
	clusters    []namedCluster
	failGroups  bool
	groupCalls  atomic.Int32
	detailCalls atomic.Int32
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/kubernetes/clusters/":
			var matched []string
			name := r.URL.Query().Get("name")
			for _, c := range f.clusters {
				if name == "" || name == c.name {
					matched = append(matched, c.body)
				}
			}
			_, _ = fmt.Fprintf(w, `{"count":%d,"current":1,"next":null,"previous":null,"last":1,"results":[%s]}`,
				len(matched), strings.Join(matched, ","))
		case "/api/kubernetes/clusters/c-prod/":
			f.detailCalls.Add(1)
			_, _ = w.Write([]byte(prodCluster))
		case "/api/iam/groups/":
			f.groupCalls.Add(1)
			if f.failGroups {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"count":2,"current":1,"next":null,"previous":null,"last":1,
				"results":[{"id":"g-1","name":"platform"},{"id":"g-2","name":"sre"}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func setupKubeCommandConfig(t *testing.T, workspaceURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(workspaceURL, "ws", "token", "", "", "", "", 0, false))
}

func withOutputFormat(t *testing.T, format string) {
	t.Helper()
	old := utils.OutputFormat
	utils.OutputFormat = format
	t.Cleanup(func() { utils.OutputFormat = old })
}

func TestKubeListPrintsTable(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod, fresh}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	stdout, stderr, code := runKubeCommandWith(t, apiClientTo(ts), utils.OutputFormatTable, "ls")

	require.Equal(t, 0, code, stderr)

	assert.Regexp(t, `NAME\s+PROVIDER\s+KUBE VERSION\s+NODES\s+CONNECTED\s+ENABLED\s+AGENT VERSION`, stdout)
	assert.Regexp(t, `prod-east\s+eks\s+v1\.31\.2\s+3\s+true\s+true\s+0\.1\.0`, stdout)
	assert.Regexp(t, `fresh\s+unknown\s+false\s+true`, stdout)
}

func TestKubeListJSON(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod, fresh}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	stdout, stderr, code := runKubeCommandWith(t, apiClientTo(ts), utils.OutputFormatJSON, "ls")

	require.Equal(t, 0, code, stderr)

	assert.JSONEq(t, `[
		{"name":"prod-east","provider":"eks","kube_version":"v1.31.2","node_count":3,"connected":true,"enabled":true,"agent_version":"0.1.0"},
		{"name":"fresh","provider":"unknown","kube_version":null,"node_count":null,"connected":false,"enabled":true,"agent_version":""}
	]`, stdout)
}

func TestKubeListEmptyJSON(t *testing.T) {
	f := &fakeServer{}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	stdout, stderr, code := runKubeCommandWith(t, apiClientTo(ts), utils.OutputFormatJSON, "ls")

	require.Equal(t, 0, code, stderr)

	assert.JSONEq(t, `[]`, stdout)
}

func TestKubeDescribePrintsRows(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	stdout, stderr, code := runKubeCommandWith(t, apiClientTo(ts), utils.OutputFormatTable, "describe", "prod-east")

	require.Equal(t, 0, code, stderr)

	assert.Regexp(t, `FIELD\s+VALUE`, stdout)
	assert.Regexp(t, `ID\s+c-prod`, stdout)
	assert.Regexp(t, `Kube version\s+v1\.31\.2`, stdout)
	assert.Regexp(t, `Nodes\s+3`, stdout)
	assert.Regexp(t, `Connected\s+true`, stdout)
	assert.Regexp(t, `Groups\s+platform, sre`, stdout)
	assert.Regexp(t, `Tags\s+env=prod, team=infra`, stdout)
	assert.Equal(t, int32(1), f.groupCalls.Load())
}

func TestKubeDescribeJSONIsRawBody(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	stdout, stderr, code := runKubeCommandWith(t, apiClientTo(ts), utils.OutputFormatJSON, "describe", "prod-east")

	require.Equal(t, 0, code, stderr)

	assert.JSONEq(t, prodCluster, stdout)
	assert.Equal(t, int32(1), f.detailCalls.Load())
	assert.Equal(t, int32(0), f.groupCalls.Load(), "the raw response keeps group IDs, so no name lookup is needed")
}

func TestKubeDescribeGroupLookupFailureShowsUUIDs(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod}, failGroups: true}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	stdout, stderr, code := runKubeCommandWith(t, apiClientTo(ts), utils.OutputFormatTable, "describe", "prod-east")

	require.Equal(t, 0, code, stderr)

	assert.Regexp(t, `Groups\s+g-1, g-2`, stdout)
	assert.Contains(t, stderr, "Could not resolve group names")
}

func TestKubeCommandNamesAndAliases(t *testing.T) {
	assert.Equal(t, "kube", KubeCmd.Name())
	assert.Equal(t, []string{"k8s", "clusters"}, KubeCmd.Aliases)
	for name, aliases := range map[string][]string{"ls": {"list"}, "describe": {"desc"}} {
		sub, _, err := KubeCmd.Find([]string{name})
		require.NoError(t, err)
		assert.Equal(t, aliases, sub.Aliases)
	}

	for _, path := range [][]string{{"ls"}, {"list"}, {"describe"}, {"desc"}, {"config"}, {"login"}, {"credentials"}, {"proxy"}, {"logout"}, {"sessions"}} {
		found, _, err := KubeCmd.Find(path)
		require.NoError(t, err, "%v", path)
		assert.NotSame(t, KubeCmd, found, "%v must resolve to a subcommand", path)
	}
}

func TestKubeConfigNextStepNamesAFileKubectlDoesNotRead(t *testing.T) {
	dir := t.TempDir()
	read := filepath.Join(dir, "read")
	t.Setenv("KUBECONFIG", read)
	withOutputFormat(t, utils.OutputFormatTable)

	tests := []struct {
		name    string
		written string
		want    string
	}{
		{name: "file kubectl reads", written: read, want: "Next: kubectl get pods -A"},
		{name: "separate file", written: filepath.Join(dir, "alpacon"), want: "Next: kubectl --kubeconfig " + shellQuote(filepath.Join(dir, "alpacon")) + " get pods -A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr := testutil.CaptureOutput(t, func() {
				_ = printKubeConfigResult(os.Stdout, &kubeConfigResult{Context: "alpacon-prod-east", Kubeconfig: tt.written}, false)
			})

			assert.Contains(t, stderr, tt.want)
		})
	}
}

func TestShellQuoteFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		goos string
		in   string
		want string
	}{
		{name: "plain path", goos: "linux", in: "/home/alice/.kube/alpacon", want: "/home/alice/.kube/alpacon"},
		{name: "space", goos: "linux", in: "/tmp/my kube", want: "'/tmp/my kube'"},
		{name: "single quote", goos: "darwin", in: "/tmp/it's", want: `'/tmp/it'\''s'`},
		{name: "leading tilde stays literal", goos: "linux", in: "~/kc", want: "'~/kc'"},
		{name: "dollar sign", goos: "linux", in: "/tmp/$HOME", want: "'/tmp/$HOME'"},
		{name: "empty", goos: "linux", in: "", want: "''"},
		{name: "windows plain path", goos: "windows", in: `C:\Users\alice\.kube\alpacon`, want: `C:\Users\alice\.kube\alpacon`},
		{name: "windows space", goos: "windows", in: `C:\Users\Alice Kim\kc`, want: `"C:\Users\Alice Kim\kc"`},
		{name: "windows apostrophe", goos: "windows", in: `C:\Users\o'brien\kc`, want: `"C:\Users\o'brien\kc"`},
		{name: "windows empty", goos: "windows", in: "", want: `""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, shellQuoteFor(tt.goos, tt.in))
		})
	}
}

func TestKubeConfigWarnsOnlyWhenAnEarlierFileCanOverride(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	t.Setenv("KUBECONFIG", first+string(os.PathListSeparator)+second)
	withOutputFormat(t, utils.OutputFormatTable)

	tests := []struct {
		name    string
		written string
		warned  bool
	}{
		{name: "first file", written: first},
		{name: "later file", written: second, warned: true},
		{name: "file outside KUBECONFIG", written: filepath.Join(dir, "other")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr := testutil.CaptureOutput(t, func() {
				_ = printKubeConfigResult(os.Stdout, &kubeConfigResult{Context: "alpacon-prod-east", Kubeconfig: tt.written}, false)
			})

			if tt.warned {
				assert.Contains(t, stderr, "KUBECONFIG lists files before")
			} else {
				assert.NotContains(t, stderr, "KUBECONFIG lists files before")
			}
		})
	}
}

// Serial: it swaps os.Stdout and os.Stderr and flips utils.OutputFormat.
func TestKubeConfigWarnsWhenTheSessionOpenedOnADriftedCluster(t *testing.T) {
	const (
		complete   = `{"cluster_external_grants":2,"namespaced_external_grants":1,"incomplete":false,"checked_at":"2026-11-10T09:00:00Z","policy":"warn"}`
		incomplete = `{"cluster_external_grants":0,"namespaced_external_grants":3,"incomplete":true,"checked_at":"2026-11-10T09:00:00Z","policy":"warn"}`
	)
	tests := []struct {
		name    string
		format  string
		warning string
		want    string // "" means no warning line at all
	}{
		{name: "no warning", format: utils.OutputFormatTable},
		{name: "null warning", format: utils.OutputFormatTable, warning: `null`},
		{
			name: "warning", format: utils.OutputFormatTable, warning: complete,
			want: `Warning: Cluster "prod-east" has RBAC bindings made outside Alpacon that widen what this session's identity may do (external grants: 2 cluster-wide, 1 namespaced); the workspace policy allows the session anyway.`,
		},
		{
			name: "warning under json output", format: utils.OutputFormatJSON, warning: complete,
			want: `Warning: Cluster "prod-east" has RBAC bindings made outside Alpacon that widen what this session's identity may do (external grants: 2 cluster-wide, 1 namespaced); the workspace policy allows the session anyway.`,
		},
		{
			name: "incomplete check", format: utils.OutputFormatTable, warning: incomplete,
			want: `Warning: Cluster "prod-east" has RBAC bindings made outside Alpacon that widen what this session's identity may do (external grants: at least 3 namespaced; the cluster's check did not finish); the workspace policy allows the session anyway.`,
		},
		{
			name: "warning that does not decode", format: utils.OutputFormatTable, warning: `"degraded"`,
			want: `Warning: Cluster "prod-east" has RBAC bindings made outside Alpacon that widen what this session's identity may do; the workspace policy allows the session anyway.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withOutputFormat(t, tt.format)
			result := kubeLoginResult{kubeConfigResult: kubeConfigResult{
				Context: "alpacon-prod-east", Kubeconfig: filepath.Join(t.TempDir(), "config"), clusterName: "prod-east",
			}}
			if tt.warning != "" {
				result.RBACDriftWarning = json.RawMessage(tt.warning)
			}
			for _, p := range []struct {
				name string
				run  func()
			}{
				{name: "kube config", run: func() { _ = printKubeConfigResult(os.Stdout, &result.kubeConfigResult, false) }},
				{name: "kube login", run: func() { _ = printKubeLoginResult(os.Stdout, result, false, time.Now()) }},
			} {
				stdout, stderr := testutil.CaptureOutput(t, p.run)
				if tt.want == "" {
					assert.NotContains(t, stderr, "RBAC", p.name)
				} else {
					assert.Contains(t, stderr, tt.want+"\n", p.name)
					assert.NotContains(t, stdout, "Warning:", p.name)
				}
				if tt.format == utils.OutputFormatJSON {
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(stdout), &fields), p.name)
					assert.JSONEq(t, tt.warning, string(fields["rbac_drift_warning"]), p.name)
				}
			}
		})
	}
}
