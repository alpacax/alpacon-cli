package kubernetes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}
}

func ptr[T any](v T) *T { return &v }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestGetClusterList_WalksAllPages(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int32

	// 150 clusters across two pages; the server caps page_size at 100.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count > 3 {
			t.Errorf("infinite loop detected: request #%d (page param: %s)", count, r.URL.Query().Get("page"))
			return
		}
		if r.URL.Path != clusterURL {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var results []ClusterDetail
		var next int
		switch r.URL.Query().Get("page") {
		case "1", "":
			for i := range 100 {
				results = append(results, ClusterDetail{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("cluster-%d", i)})
			}
			next = 2
		case "2":
			for i := range 50 {
				results = append(results, ClusterDetail{ID: fmt.Sprintf("id-p2-%d", i), Name: fmt.Sprintf("cluster-p2-%d", i)})
			}
		}
		writeJSON(w, api.ListResponse[ClusterDetail]{Count: 150, Next: next, Results: results})
	}))
	defer ts.Close()

	clusters, err := GetClusterList(newTestClient(ts))
	require.NoError(t, err)
	assert.Len(t, clusters, 150)
	assert.Equal(t, "cluster-0", clusters[0].Name)
	assert.Equal(t, "cluster-p2-49", clusters[149].Name)
	assert.Equal(t, int32(2), requestCount.Load())
}

func TestGetClusterList_Projection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cluster  string
		expected ClusterAttributes
	}{
		{
			name: "measured cluster",
			cluster: `{"id":"c1","name":"prod-east","provider":"eks","kube_version":"v1.31.2","agent_version":"0.1.0",
				"node_count":3,"status":null,"enabled":true,"groups":[],"tags":{},"is_connected":true}`,
			expected: ClusterAttributes{Name: "prod-east", Provider: "eks", KubeVersion: Optional[string]{Value: ptr("v1.31.2")}, NodeCount: Optional[int]{Value: ptr(3)}, Connected: true, Enabled: true, AgentVersion: "0.1.0"},
		},
		{
			name: "not yet measured",
			cluster: `{"id":"c2","name":"fresh","provider":"unknown","kube_version":null,"agent_version":"",
				"node_count":null,"status":null,"enabled":true,"groups":[],"tags":{},"is_connected":false}`,
			expected: ClusterAttributes{Name: "fresh", Provider: "unknown", Enabled: true},
		},
		{
			name: "measured with no nodes and disabled",
			cluster: `{"id":"c3","name":"empty","provider":"k3s","kube_version":"","agent_version":"0.1.0",
				"node_count":0,"status":null,"enabled":false,"groups":[],"tags":{},"is_connected":false}`,
			expected: ClusterAttributes{Name: "empty", Provider: "k3s", KubeVersion: Optional[string]{Value: ptr("")}, NodeCount: Optional[int]{Value: ptr(0)}, AgentVersion: "0.1.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"count":1,"current":1,"next":null,"previous":null,"last":1,"results":[%s]}`, tt.cluster)
			}))
			defer ts.Close()

			clusters, err := GetClusterList(newTestClient(ts))
			require.NoError(t, err)
			assert.Equal(t, []ClusterAttributes{tt.expected}, clusters)
		})
	}
}

func TestOptional(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		value      any
		expectText string
		expectJSON string
	}{
		{name: "null string", value: Optional[string]{}, expectText: "", expectJSON: "null"},
		{name: "empty string", value: Optional[string]{Value: ptr("")}, expectText: "", expectJSON: `""`},
		{name: "version", value: Optional[string]{Value: ptr("v1.31.2")}, expectText: "v1.31.2", expectJSON: `"v1.31.2"`},
		{name: "null int", value: Optional[int]{}, expectText: "", expectJSON: "null"},
		{name: "zero nodes", value: Optional[int]{Value: ptr(0)}, expectText: "0", expectJSON: "0"},
		{name: "three nodes", value: Optional[int]{Value: ptr(3)}, expectText: "3", expectJSON: "3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// PrintTable renders each cell with %v.
			assert.Equal(t, tt.expectText, fmt.Sprintf("%v", tt.value))
			data, err := json.Marshal(tt.value)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectJSON, string(data))
		})
	}
}

func TestGetClusterList_Empty(t *testing.T) {
	t.Parallel()
	// A service token always gets an empty list.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.ListResponse[ClusterDetail]{Results: []ClusterDetail{}})
	}))
	defer ts.Close()

	clusters, err := GetClusterList(newTestClient(ts))
	require.NoError(t, err)
	assert.Empty(t, clusters)
}

func TestGetClusterList_KeepsNotFoundStatus(t *testing.T) {
	t.Parallel()
	// A server with the Kubernetes surface turned off answers 404; the command
	// layer tells that apart by status, so the code must survive the wrapping.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not found."}`))
	}))
	defer ts.Close()

	_, err := GetClusterList(newTestClient(ts))
	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, utils.HTTPStatusCode(err))
}

func TestGetClusterIDByName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		expectedID  string
		expectErr   string
		expectBlank bool
	}{
		{name: "found", input: "prod-east", expectedID: "c-prod"},
		{name: "padded name", input: "  prod-east  ", expectedID: "c-prod"},
		{name: "not found", input: "missing", expectErr: "no cluster found with the given name"},
		{name: "whitespace only", input: "   ", expectBlank: true},
		{name: "empty", input: "", expectBlank: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var called atomic.Bool
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Store(true)
				if r.URL.Path != clusterURL {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				results := []ClusterDetail{}
				if r.URL.Query().Get("name") == "prod-east" {
					results = append(results, ClusterDetail{ID: "c-prod", Name: "prod-east"})
				}
				writeJSON(w, api.ListResponse[ClusterDetail]{Count: len(results), Results: results})
			}))
			defer ts.Close()

			id, err := GetClusterIDByName(newTestClient(ts), tt.input)
			switch {
			case tt.expectBlank:
				require.ErrorIs(t, err, api.ErrBlankName)
				assert.False(t, called.Load(), "a blank name must be refused before any request")
			case tt.expectErr != "":
				require.EqualError(t, err, tt.expectErr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.expectedID, id)
			}
		})
	}
}

const detailBody = `{
	"id": "c-prod",
	"name": "prod-east",
	"provider": "eks",
	"kube_version": "v1.31.2",
	"agent_version": "0.1.0",
	"node_count": 3,
	"status": {"phase": "ready"},
	"enabled": true,
	"groups": ["g-1", "g-2"],
	"tags": {"env": "prod"},
	"is_connected": true,
	"last_connectivity": "2026-09-30T08:15:42.123456+09:00",
	"last_synced_at": null,
	"added_at": "2026-09-18T01:02:03.000001Z",
	"updated_at": "2026-09-30T00:00:00Z"
}`

func TestGetClusterDetail_Decodes(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != clusterURL+"c-prod/" {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(detailBody))
	}))
	defer ts.Close()

	cluster, err := GetClusterDetail(newTestClient(ts), "c-prod")
	require.NoError(t, err)

	require.NotNil(t, cluster.KubeVersion)
	assert.Equal(t, "v1.31.2", *cluster.KubeVersion)
	require.NotNil(t, cluster.NodeCount)
	assert.Equal(t, 3, *cluster.NodeCount)
	assert.JSONEq(t, `{"phase":"ready"}`, string(cluster.Status))
	assert.JSONEq(t, `{"env":"prod"}`, string(cluster.Tags))
	assert.Equal(t, []string{"g-1", "g-2"}, cluster.Groups)
	require.NotNil(t, cluster.LastConnectivity)
	assert.True(t, cluster.LastConnectivity.Equal(time.Date(2026, 9, 29, 23, 15, 42, 123456000, time.UTC)), "last_connectivity: %v", cluster.LastConnectivity)
	assert.Nil(t, cluster.LastSyncedAt)
	assert.True(t, cluster.AddedAt.Equal(time.Date(2026, 9, 18, 1, 2, 3, 1000, time.UTC)), "added_at: %v", cluster.AddedAt)
}

func TestGetClusterDetailRaw_ReturnsBodyUnchanged(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(detailBody))
	}))
	defer ts.Close()

	body, err := GetClusterDetailRaw(newTestClient(ts), "c-prod")
	require.NoError(t, err)
	assert.JSONEq(t, detailBody, string(body))
}

func TestResolveGroupNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		ids         []string
		failLookup  bool
		expected    []string
		expectCalls int32
	}{
		{name: "all known", ids: []string{"g-1", "g-2"}, expected: []string{"platform", "sre"}, expectCalls: 1},
		{name: "unknown kept as UUID", ids: []string{"g-1", "g-unknown"}, expected: []string{"platform", "g-unknown"}, expectCalls: 1},
		{name: "lookup failure falls back to UUIDs", ids: []string{"g-1"}, failLookup: true, expected: []string{"g-1"}, expectCalls: 1},
		{name: "no groups makes no request", ids: nil, expected: nil, expectCalls: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != iamGroupURL {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				if tt.failLookup {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				writeJSON(w, api.ListResponse[groupSummary]{Count: 2, Results: []groupSummary{
					{ID: "g-1", Name: "platform"},
					{ID: "g-2", Name: "sre"},
				}})
			}))
			defer ts.Close()

			assert.Equal(t, tt.expected, ResolveGroupNames(newTestClient(ts), tt.ids))
			assert.Equal(t, tt.expectCalls, calls.Load())
		})
	}
}

const sessionBody = `{
	"id": "s-1",
	"cluster": "c-prod",
	"channel_id": "ch-1",
	"channel_token": "tok-secret",
	"token_expired_at": "2026-10-08T12:30:00.000001+09:00",
	"proxy_url": "https://kube.example.com"
}`

func TestCreateSession_Body(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		workSession string
		expectBody  string
	}{
		{
			name:        "with work session",
			workSession: "ws-1",
			expectBody:  `{"cluster":"c-prod","work_session":"ws-1","client_type":"cli"}`,
		},
		{
			name:       "without work session",
			expectBody: `{"cluster":"c-prod","client_type":"cli"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bodies := make(chan string, 1)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != sessionURL {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				bodies <- string(body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(sessionBody))
			}))
			defer ts.Close()

			_, err := CreateSession(newTestClient(ts), KubeSessionCreateRequest{
				Cluster:     "c-prod",
				WorkSession: tt.workSession,
				ClientType:  ClientTypeCLI,
			})
			require.NoError(t, err)
			require.Len(t, bodies, 1)
			// JSONEq over the whole body also pins that work_session is absent,
			// not sent empty, when no work session is set.
			assert.JSONEq(t, tt.expectBody, <-bodies)
		})
	}
}

func TestCreateSession_DecodesResponse(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(sessionBody))
	}))
	defer ts.Close()

	session, err := CreateSession(newTestClient(ts), KubeSessionCreateRequest{Cluster: "c-prod", ClientType: ClientTypeCLI})
	require.NoError(t, err)
	require.NotNil(t, session)

	assert.Equal(t, "s-1", session.ID)
	assert.Equal(t, "c-prod", session.Cluster)
	assert.Equal(t, "ch-1", session.ChannelID)
	assert.Equal(t, "tok-secret", session.ChannelToken)
	assert.Equal(t, "https://kube.example.com", session.ProxyURL)
	assert.True(t, session.TokenExpiredAt.Equal(time.Date(2026, 10, 8, 3, 30, 0, 1000, time.UTC)), "token_expired_at: %v", session.TokenExpiredAt)
}

func TestCreateSession_RefusesAnUnusableCreatedID(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"../x"}`))
	}))
	defer ts.Close()

	session, err := CreateSession(newTestClient(ts), KubeSessionCreateRequest{})

	require.ErrorIs(t, err, api.ErrUnusableID)
	require.NotErrorIs(t, err, utils.ErrInvalidID)
	assert.Nil(t, session)
	assert.Contains(t, err.Error(), "the server created the kube session")
}

func TestCreateSession_TrimsTheCreatedID(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":" s-1 "}`))
	}))
	defer ts.Close()

	session, err := CreateSession(newTestClient(ts), KubeSessionCreateRequest{})

	require.NoError(t, err)
	assert.Equal(t, "s-1", session.ID)
}

func TestCreateSession_KeepsErrorStatusAndCode(t *testing.T) {
	t.Parallel()
	// The command layer branches on the refusal's status and code, so both
	// must survive the client's wrapping.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"permission_denied","source":"kubernetes"}`))
	}))
	defer ts.Close()

	session, err := CreateSession(newTestClient(ts), KubeSessionCreateRequest{Cluster: "c-prod", ClientType: ClientTypeCLI})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(err))
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, "permission_denied", code)
}

func TestCloseSession_PostsToClosePath(t *testing.T) {
	t.Parallel()
	requests := make(chan string, 1)
	bodies := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		requests <- r.Method + " " + r.URL.Path
		bodies <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	require.NoError(t, CloseSession(newTestClient(ts), "s-1"))
	require.Len(t, requests, 1)
	assert.Equal(t, "POST /api/kubernetes/sessions/s-1/close/", <-requests)
	// An empty object, not null: the action takes no fields.
	assert.JSONEq(t, `{}`, <-bodies)
}

func TestCloseSession_RefusesIDThatWouldChangeTheRoute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		sessionID string
	}{
		{name: "empty", sessionID: ""},
		{name: "blank", sessionID: "  "},
		{name: "dot", sessionID: "."},
		{name: "dot dot", sessionID: ".."},
		{name: "parent segment", sessionID: "../clusters/c-1"},
		{name: "nested segment", sessionID: "s-1/x"},
		{name: "backslash", sessionID: `s-1\x`},
		{name: "encoded slash", sessionID: "s-1%2Fx"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer ts.Close()

			require.ErrorIs(t, CloseSession(newTestClient(ts), tt.sessionID), utils.ErrInvalidID)
			assert.Zero(t, hits.Load())
		})
	}
}

func TestClusterCallsRefuseAPathTraversingIDBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	const badID = "../../etc/test"
	tests := []struct {
		name string
		call func(ac *client.AlpaconClient) error
	}{
		{"GetClusterDetailRaw", func(ac *client.AlpaconClient) error {
			_, err := GetClusterDetailRaw(ac, badID)
			return err
		}},
		{"GetClusterDetail", func(ac *client.AlpaconClient) error {
			_, err := GetClusterDetail(ac, badID)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			err := tt.call(ac)

			require.ErrorIs(t, err, utils.ErrInvalidID)
		})
	}
}

func TestCloseSession_PropagatesError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not found."}`))
	}))
	defer ts.Close()

	err := CloseSession(newTestClient(ts), "s-gone")
	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, utils.HTTPStatusCode(err))
}

func TestGetClusterIDByName_ReturnsOnlyAOneSegmentID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, id, want string }{
		{name: "padded_id", id: " id-1 ", want: "id-1"},
		{name: "parent_traversal", id: "../x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.ListResponse[ClusterDetail]{Count: 1, Results: []ClusterDetail{{ID: tc.id, Name: "my-cluster"}}})
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			id, err := GetClusterIDByName(ac, "my-cluster")

			if tc.want == "" {
				require.ErrorIs(t, err, api.ErrUnusableID)
				assert.Empty(t, id)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, id)
		})
	}
}
