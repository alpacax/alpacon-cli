package kubernetes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
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
