package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression for #274: a cursor-token next crashed the old int-typed unmarshal.
func TestGetAuditLogList_FollowsCursor(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(api.CursorListResponse[AuditLogEntry]{
				Next:    "eyJzIjpbMV0sImQiOiJhZnRlciJ9",
				Results: []AuditLogEntry{{Username: "alice", App: "cert"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.CursorListResponse[AuditLogEntry]{
			Results: []AuditLogEntry{{Username: "bob", App: "iam"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	logs, err := GetAuditLogList(ac, 5, "", "", "")
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "bob", logs[1].Username)
}

func TestGetAuditLogList_Filters(t *testing.T) {
	t.Parallel()
	var gotQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/iam/users") {
			assert.Equal(t, "alice", r.URL.Query().Get("username"))
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"user-1"}]}`))
			return
		}
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(api.CursorListResponse[AuditLogEntry]{
			Results: []AuditLogEntry{{Username: "alice"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	logs, err := GetAuditLogList(ac, 5, "alice", "cert", "certificate")
	require.NoError(t, err)
	assert.Len(t, logs, 1)
	// The username filter is resolved to a user ID before hitting the audit endpoint.
	assert.Equal(t, "user-1", gotQuery.Get("user"))
	assert.Equal(t, "cert", gotQuery.Get("app"))
	assert.Equal(t, "certificate", gotQuery.Get("model"))
}
