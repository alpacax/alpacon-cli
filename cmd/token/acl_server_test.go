package token

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	serverapi "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeServerResults(w http.ResponseWriter, results []serverapi.ServerDetails) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(api.ListResponse[serverapi.ServerDetails]{Count: len(results), Results: results})
}

func TestResolveServerIDs(t *testing.T) {
	t.Parallel()
	servers := map[string]string{"web-01": "id-web", "db-01": "id-db"}

	tests := []struct {
		name    string
		names   []string
		wantIDs []string
		wantErr string
	}{
		{"positions preserved", []string{"db-01", "web-01"}, []string{"id-db", "id-web"}, ""},
		{"duplicates resolved each time", []string{"web-01", "db-01", "web-01"}, []string{"id-web", "id-db", "id-web"}, ""},
		{"blank name refused", []string{"web-01", "  "}, nil, "failed to resolve server '  ': server name is required"},
		{"missing name reported", []string{"web-01", "ghost"}, nil, "failed to resolve server 'ghost': no server found with the given name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := r.URL.Query().Get("name")
				var results []serverapi.ServerDetails
				if id, ok := servers[name]; ok {
					results = []serverapi.ServerDetails{{ID: id, Name: name}}
				}
				writeServerResults(w, results)
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			ids, err := resolveServerIDs(ac, tt.names)

			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantIDs, ids)
		})
	}
}

// Serial: the test holds one response back to invert completion order, and a shared core
// would blur that window.
func TestResolveServerIDsReportsEarliestFailureInInputOrder(t *testing.T) {
	laterAnswered := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("name") {
		case "missing-early":
			select {
			case <-laterAnswered:
				time.Sleep(100 * time.Millisecond)
			case <-time.After(2 * time.Second):
			}
			writeServerResults(w, nil)
		case "missing-late":
			writeServerResults(w, nil)
			close(laterAnswered)
		default:
			writeServerResults(w, []serverapi.ServerDetails{{ID: "id-web", Name: "web-01"}})
		}
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := resolveServerIDs(ac, []string{"missing-early", "web-01", "missing-late"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "'missing-early'")
}

// Serial for the same reason: concurrency is measured by overlapping handler windows.
func TestResolveServerIDsBoundsConcurrentRequests(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		name := r.URL.Query().Get("name")
		writeServerResults(w, []serverapi.ServerDetails{{ID: "id-" + name, Name: name}})
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	names := make([]string, 20)
	for i := range names {
		names[i] = "srv-" + string(rune('a'+i))
	}

	ids, err := resolveServerIDs(ac, names)

	require.NoError(t, err)
	assert.Equal(t, "id-srv-a", ids[0])
	assert.Equal(t, "id-srv-t", ids[19])
	assert.LessOrEqual(t, maxInFlight.Load(), int32(maxConcurrentServerLookups))
}
