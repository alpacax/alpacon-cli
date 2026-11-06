package kube

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	openSessionJSON = `{"id":"s-1","cluster":"c-prod","user":{"id":"u-1","name":"alice","email":"alice@example.com"},
		"work_session":{"id":"w-1"},"token_expired_at":"2026-11-02T11:00:00Z","closed_at":null}`
	closedSessionJSON = `{"id":"s-2","cluster":"c-gone","user":{"id":"u-2","name":"","email":"bob@example.com"},
		"token_expired_at":"2026-11-01T11:00:00Z","closed_at":"2026-11-01T10:00:00Z"}`
)

// sessionListServer answers the session and cluster lists and records each query.
type sessionListServer struct {
	mu           sync.Mutex
	sessions     []string
	listQueries  []url.Values
	clusterCalls int
	failClusters bool
}

func (f *sessionListServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/kubernetes/sessions/":
			f.listQueries = append(f.listQueries, r.URL.Query())
			_, _ = fmt.Fprintf(w, `{"count":%d,"current":1,"next":null,"previous":null,"last":1,"results":[%s]}`, len(f.sessions), strings.Join(f.sessions, ","))
		case "/api/kubernetes/clusters/":
			f.clusterCalls++
			if f.failClusters {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			name := r.URL.Query().Get("name")
			if name != "" && name != "prod-east" {
				_, _ = w.Write([]byte(`{"count":0,"current":1,"next":null,"previous":null,"last":1,"results":[]}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"count":1,"current":1,"next":null,"previous":null,"last":1,"results":[%s]}`, prodCluster)
		default:
			t.Errorf("unexpected request %s", r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
}

func newSessionClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
}

func TestListKubeSessionsFiltersOnTheServer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		cluster     string
		all         bool
		wantCluster []string
		wantStatus  []string
	}{
		{name: "open by default", wantStatus: []string{"open"}},
		{name: "all drops the status", all: true},
		{name: "cluster name goes as its ID", cluster: "prod-east", wantCluster: []string{"c-prod"}, wantStatus: []string{"open"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &sessionListServer{sessions: []string{openSessionJSON}}
			ts := httptest.NewServer(f.handler(t))
			defer ts.Close()

			rows, err := listKubeSessions(newSessionClient(ts), tt.cluster, tt.all)

			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, "prod-east", rows[0].Cluster)
			require.Len(t, f.listQueries, 1)
			assert.Equal(t, tt.wantCluster, f.listQueries[0]["cluster"])
			assert.Equal(t, tt.wantStatus, f.listQueries[0]["status"])
		})
	}
}

func TestListKubeSessionsUnknownClusterFailsBeforeTheList(t *testing.T) {
	t.Parallel()
	f := &sessionListServer{}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()

	_, err := listKubeSessions(newSessionClient(ts), "missing", false)

	var lookupErr *clusterLookupError
	require.ErrorAs(t, err, &lookupErr)
	assert.Empty(t, f.listQueries)
}

func TestListKubeSessionsAskClusterNamesOnlyWhenNeeded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		cluster      string
		sessions     []string
		failClusters bool
		wantCalls    int
		wantRows     int
		wantCluster  string
	}{
		{name: "an empty list needs no names", wantCalls: 0, wantRows: 0},
		{name: "rows need one read", sessions: []string{openSessionJSON}, wantCalls: 1, wantRows: 1, wantCluster: "prod-east"},
		{name: "a named cluster needs only its lookup", cluster: "prod-east", sessions: []string{openSessionJSON}, wantCalls: 1, wantRows: 1, wantCluster: "prod-east"},
		{name: "a failed read shows the ID", sessions: []string{openSessionJSON}, failClusters: true, wantCalls: 1, wantRows: 1, wantCluster: "c-prod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &sessionListServer{sessions: tt.sessions, failClusters: tt.failClusters}
			ts := httptest.NewServer(f.handler(t))
			defer ts.Close()

			rows, err := listKubeSessions(newSessionClient(ts), tt.cluster, false)

			require.NoError(t, err)
			assert.Equal(t, tt.wantCalls, f.clusterCalls)
			require.Len(t, rows, tt.wantRows)
			if tt.wantCluster != "" {
				assert.Equal(t, tt.wantCluster, rows[0].Cluster)
			}
		})
	}
}

func resetKubeSessionsFlags(t *testing.T, cluster string, all bool) {
	t.Helper()
	oldCluster, oldAll := kubeSessionsCluster, kubeSessionsAll
	kubeSessionsCluster, kubeSessionsAll = cluster, all
	t.Cleanup(func() { kubeSessionsCluster, kubeSessionsAll = oldCluster, oldAll })
}

// Serial: it sets HOME, swaps os.Stdout and flips utils.OutputFormat.
func TestKubeSessionsPrintsTable(t *testing.T) {
	f := &sessionListServer{sessions: []string{openSessionJSON, closedSessionJSON}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatTable)
	resetKubeSessionsFlags(t, "", true)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeSessionsCmd.Run(kubeSessionsCmd, nil)
	})

	assert.Regexp(t, `ID\s+CLUSTER\s+USER\s+STATUS\s+EXPIRES AT`, stdout)
	assert.Regexp(t, `s-1\s+prod-east\s+alice\s+open\s+2026-11-0`, stdout)
	assert.Regexp(t, `s-2\s+c-gone\s+bob@example\.com\s+closed\s+2026-11-0`, stdout)
	assert.NotContains(t, stdout, "CLUSTER ID")
}

// Serial: it sets HOME, swaps os.Stdout and flips utils.OutputFormat.
func TestKubeSessionsJSON(t *testing.T) {
	f := &sessionListServer{sessions: []string{openSessionJSON}}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatJSON)
	resetKubeSessionsFlags(t, "", false)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeSessionsCmd.Run(kubeSessionsCmd, nil)
	})

	// The local-time format itself is pinned by TestProjectSessions.
	expiresAt := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")
	assert.JSONEq(t, fmt.Sprintf(`[{"id":"s-1","cluster":"prod-east","cluster_id":"c-prod","user":"alice","status":"open",
		"expires_at":%q,"token_expired_at":"2026-11-02T11:00:00Z","closed_at":null}]`, expiresAt), stdout)
}

// Serial: it sets HOME, swaps os.Stdout and flips utils.OutputFormat.
func TestKubeSessionsEmptyJSON(t *testing.T) {
	f := &sessionListServer{}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	setupKubeCommandConfig(t, ts.URL)
	withOutputFormat(t, utils.OutputFormatJSON)
	resetKubeSessionsFlags(t, "", false)

	stdout, _ := testutil.CaptureOutput(t, func() {
		kubeSessionsCmd.Run(kubeSessionsCmd, nil)
	})

	assert.JSONEq(t, `[]`, stdout)
}
