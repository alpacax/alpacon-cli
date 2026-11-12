package kubernetes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSessionList_SendsOnlyTheFiltersGiven(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		clusterID   string
		status      string
		wantCluster []string
		wantStatus  []string
	}{
		{name: "both", clusterID: "c-1", status: SessionStatusOpen, wantCluster: []string{"c-1"}, wantStatus: []string{"open"}},
		{name: "status only", status: SessionStatusOpen, wantStatus: []string{"open"}},
		{name: "neither"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var got url.Values
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				got = r.URL.Query()
				mu.Unlock()
				writeJSON(w, api.ListResponse[KubeSessionListItem]{})
			}))
			defer ts.Close()

			_, err := GetSessionList(newTestClient(ts), tt.clusterID, tt.status)
			require.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tt.wantCluster, got["cluster"])
			assert.Equal(t, tt.wantStatus, got["status"])
		})
	}
}

func TestGetSessionList_WalksPagesAndDecodesTheServerShape(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"count":2,"next":0,"results":[{"id":"s-2","cluster":"c-1","user":null,"token_expired_at":null,"closed_at":"2026-11-02T10:00:00Z"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":2,"next":2,"results":[{"id":"s-1","cluster":"c-1","user":{"id":"u-1","name":"alice","email":"alice@example.com"},"work_session":{"id":"w-1"},"token_expired_at":"2026-11-02T11:00:00Z","latest_exec":{"id":"x-1","kind":"exec"},"closed_at":null}]}`))
	}))
	defer ts.Close()

	sessions, err := GetSessionList(newTestClient(ts), "", "")
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	assert.Equal(t, "alice", sessions[0].User.Name)
	assert.Nil(t, sessions[1].User)
	assert.NotNil(t, sessions[1].ClosedAt)
	assert.JSONEq(t, `{"id":"x-1","kind":"exec"}`, string(sessions[0].LatestExec))
	assert.Nil(t, sessions[1].LatestExec, "a page without latest_exec is what an older server sends")
}

func TestGetSessionList_BadRequestIsAnError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":["invalid_choice"]}`))
	}))
	defer ts.Close()

	_, err := GetSessionList(newTestClient(ts), "", "bad")
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, utils.HTTPStatusCode(err))
}

func TestProjectSessions(t *testing.T) {
	t.Parallel()
	expiry := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		session KubeSessionListItem
		want    KubeSessionAttributes
	}{
		{
			name:    "open with a known cluster and named user",
			session: KubeSessionListItem{ID: "s", Cluster: "c-1", User: &KubeSessionUser{Name: "alice", Email: "alice@example.com"}, TokenExpiredAt: &expiry},
			want:    KubeSessionAttributes{ID: "s", Cluster: "prod", ClusterID: "c-1", User: "alice", Status: "open", Kind: "api", ExpiresAt: utils.NewTimestamp(expiry, "2026-11-02 20:00"), TokenExpiredAt: utils.NewTimestamp(expiry, "")},
		},
		{
			name:    "closed with an unknown cluster and email only",
			session: KubeSessionListItem{ID: "s", Cluster: "c-2", User: &KubeSessionUser{Email: "alice@example.com"}, ClosedAt: &expiry},
			want:    KubeSessionAttributes{ID: "s", Cluster: "c-2", ClusterID: "c-2", User: "alice@example.com", Status: "closed", Kind: "api", ClosedAt: utils.NewTimestamp(expiry, "")},
		},
		{
			name:    "no user and no expiry",
			session: KubeSessionListItem{ID: "s", Cluster: "c-1"},
			want:    KubeSessionAttributes{ID: "s", Cluster: "prod", ClusterID: "c-1", Status: "open", Kind: "api"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ProjectSessions([]KubeSessionListItem{tt.session}, map[string]string{"c-1": "prod"}, time.FixedZone("UTC+9", 9*60*60))
			assert.Equal(t, []KubeSessionAttributes{tt.want}, got)
		})
	}
}

func TestProjectSessionsKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		latestExec string
		want       string
	}{
		{name: "field missing", want: sessionKindAPI},
		{name: "latest_exec null", latestExec: `null`, want: sessionKindAPI},
		{name: "kind exec", latestExec: `{"id":"x-1","kind":"exec"}`, want: "exec"},
		{name: "kind attach", latestExec: `{"id":"x-1","kind":"attach"}`, want: "attach"},
		{name: "kind port_forward", latestExec: `{"id":"x-1","kind":"port_forward"}`, want: "port_forward"},
		{name: "kind null", latestExec: `{"id":"x-1","kind":null}`, want: sessionKindUnknown},
		{name: "kind missing", latestExec: `{"id":"x-1"}`, want: sessionKindUnknown},
		{name: "kind empty", latestExec: `{"id":"x-1","kind":""}`, want: sessionKindUnknown},
		{name: "kind not a string", latestExec: `{"id":"x-1","kind":7}`, want: sessionKindUnknown},
		{name: "latest_exec not an object", latestExec: `"exec"`, want: sessionKindUnknown},
		{name: "kind the CLI does not know", latestExec: `{"id":"x-1","kind":"debug"}`, want: "debug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			session := KubeSessionListItem{ID: "s", Cluster: "c-1"}
			if tt.latestExec != "" {
				session.LatestExec = json.RawMessage(tt.latestExec)
			}
			got := ProjectSessions([]KubeSessionListItem{session}, nil, time.UTC)
			require.Len(t, got, 1)
			assert.Equal(t, tt.want, got[0].Kind)
		})
	}
}

// A latest_exec of a shape this build does not expect must not fail the list:
// the kind column is informational, and the rest of the row still matters.
func TestGetSessionList_MalformedLatestExecDoesNotFailTheList(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":0,"results":[{"id":"s-1","cluster":"c-1","latest_exec":["exec"],"closed_at":null}]}`))
	}))
	defer ts.Close()

	sessions, err := GetSessionList(newTestClient(ts), "", "")
	require.NoError(t, err)
	rows := ProjectSessions(sessions, nil, time.UTC)
	require.Len(t, rows, 1)
	assert.Equal(t, sessionKindUnknown, rows[0].Kind)
}
