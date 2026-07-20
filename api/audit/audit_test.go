package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression for #274: a cursor-token next crashed the old int-typed unmarshal.
func TestGetAuditLogList_FollowsCursor(t *testing.T) {
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
