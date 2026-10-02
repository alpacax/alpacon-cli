package kube

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file stay serial: they set HOME, swap os.Stdout through
// CaptureOutput, flip utils.OutputFormat, and read package-level commands.

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
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatTable)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeListCmd.Run(kubeListCmd, nil)
	})

	assert.Regexp(t, `NAME\s+PROVIDER\s+KUBE VERSION\s+NODES\s+CONNECTED\s+ENABLED\s+AGENT VERSION`, stdout)
	assert.Regexp(t, `prod-east\s+eks\s+v1\.31\.2\s+3\s+true\s+true\s+0\.1\.0`, stdout)
	assert.Regexp(t, `fresh\s+unknown\s+false\s+true`, stdout)
}

func TestKubeListJSON(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod, fresh}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatJSON)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeListCmd.Run(kubeListCmd, nil)
	})

	assert.JSONEq(t, `[
		{"name":"prod-east","provider":"eks","kube_version":"v1.31.2","node_count":3,"connected":true,"enabled":true,"agent_version":"0.1.0"},
		{"name":"fresh","provider":"unknown","kube_version":null,"node_count":null,"connected":false,"enabled":true,"agent_version":""}
	]`, stdout)
}

func TestKubeListEmptyJSON(t *testing.T) {
	f := &fakeServer{}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatJSON)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeListCmd.Run(kubeListCmd, nil)
	})

	assert.JSONEq(t, `[]`, stdout)
}

func TestKubeDescribePrintsRows(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatTable)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeDescribeCmd.Run(kubeDescribeCmd, []string{"prod-east"})
	})

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
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatJSON)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeDescribeCmd.Run(kubeDescribeCmd, []string{"prod-east"})
	})

	assert.JSONEq(t, prodCluster, stdout)
	assert.Equal(t, int32(1), f.detailCalls.Load())
	assert.Equal(t, int32(0), f.groupCalls.Load(), "the raw response keeps group IDs, so no name lookup is needed")
}

func TestKubeDescribeGroupLookupFailureShowsUUIDs(t *testing.T) {
	f := &fakeServer{clusters: []namedCluster{prod}, failGroups: true}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatTable)

	stdout, stderr := testutil.CaptureOutput(t, func() {
		kubeDescribeCmd.Run(kubeDescribeCmd, []string{"prod-east"})
	})

	assert.Regexp(t, `Groups\s+g-1, g-2`, stdout)
	assert.Contains(t, stderr, "Could not resolve group names")
}

func TestKubeCommandNamesAndAliases(t *testing.T) {
	assert.Equal(t, "kube", KubeCmd.Name())
	assert.Equal(t, []string{"k8s", "clusters"}, KubeCmd.Aliases)
	assert.Equal(t, []string{"list"}, kubeListCmd.Aliases)
	assert.Equal(t, []string{"desc"}, kubeDescribeCmd.Aliases)

	for _, path := range [][]string{{"ls"}, {"list"}, {"describe"}, {"desc"}} {
		found, _, err := KubeCmd.Find(path)
		require.NoError(t, err, "%v", path)
		assert.NotSame(t, KubeCmd, found, "%v must resolve to a subcommand", path)
	}
}
