package approval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
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

func TestListApprovalRequests(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	requests := []ApprovalRequest{
		{
			ID:          "apr-1",
			RequestType: "work_session",
			RequestData: "deploy access",
			Status:      "pending",
			RequestedBy: &types.UserSummary{ID: "u-1", Name: "alice"},
			AddedAt:     now,
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/approvals/approvals/", r.URL.Path)
		assert.Equal(t, "pending", r.URL.Query().Get("status"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[ApprovalRequest]{Count: 1, Results: requests})
	}))
	defer ts.Close()

	list, err := ListApprovalRequests(newTestClient(ts), "pending", "")
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "apr-1", list[0].ID)
	assert.Equal(t, "work_session", list[0].Type)
	assert.Equal(t, "alice", list[0].RequestedBy)
}

func TestListApprovalRequests_TypeFilter(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "sudo", r.URL.Query().Get("request_type"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[ApprovalRequest]{Count: 0, Results: nil})
	}))
	defer ts.Close()

	_, err := ListApprovalRequests(newTestClient(ts), "", "sudo")
	assert.NoError(t, err)
}

func TestListMyApprovalRequests(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/approvals/approvals/-/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[ApprovalRequest]{Count: 0, Results: nil})
	}))
	defer ts.Close()

	_, err := ListMyApprovalRequests(newTestClient(ts), "pending", "")
	assert.NoError(t, err)
}

func TestGetApprovalRequest(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/approvals/approvals/apr-abc/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ApprovalRequest{
			ID: "apr-abc", RequestType: "sudo", Status: "pending", AddedAt: now,
		})
	}))
	defer ts.Close()

	req, err := GetApprovalRequest(newTestClient(ts), "apr-abc")
	require.NoError(t, err)
	assert.Equal(t, "apr-abc", req.ID)
	assert.Equal(t, "sudo", req.RequestType)
}

func TestCancelRequest(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/approvals/approvals/apr-abc/cancel/", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	err := CancelRequest(newTestClient(ts), "apr-abc")
	assert.NoError(t, err)
}

func TestCancelRequest_403PropagatesError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "you do not have permission to perform this action"}`))
	}))
	defer ts.Close()

	err := CancelRequest(newTestClient(ts), "apr-abc")
	require.ErrorContains(t, err, "permission")
}

func TestApprovalCallsRefuseAPathTraversingIDBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	const badID = "../../etc/test"
	tests := []struct {
		name string
		call func(ac *client.AlpaconClient) error
	}{
		{"GetApprovalRequest", func(ac *client.AlpaconClient) error {
			_, err := GetApprovalRequest(ac, badID)
			return err
		}},
		{"GetApprovalRequestRaw", func(ac *client.AlpaconClient) error {
			_, err := GetApprovalRequestRaw(ac, badID)
			return err
		}},
		{"CancelRequest", func(ac *client.AlpaconClient) error { return CancelRequest(ac, badID) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := testutil.NewNoRequestServer(t)

			err := tt.call(newTestClient(ts))

			require.ErrorIs(t, err, utils.ErrInvalidID)
		})
	}
}

func TestApprovalCallsTrimAValidIDIntoOnePathSegment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func(ac *client.AlpaconClient) error
		want string
	}{
		{"GetApprovalRequestRaw", func(ac *client.AlpaconClient) error {
			_, err := GetApprovalRequestRaw(ac, " 42 ")
			return err
		}, "GET /api/approvals/approvals/42/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec atomic.Value
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.Store(r.Method + " " + r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(ts.Close)

			err := tt.call(newTestClient(ts))

			require.NoError(t, err)
			assert.Equal(t, tt.want, rec.Load())
		})
	}
}

func TestFindCommandApprovalRequestIDEarlyStop(t *testing.T) {
	t.Parallel()
	var pages atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		assert.Equal(t, myRequestsURL, r.URL.Path)
		assert.Equal(t, "pending", r.URL.Query().Get("status"))
		assert.Equal(t, "command_exec", r.URL.Query().Get("request_type"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`{"next":2,"results":[{"id":"other","metadata":{"command_id":"other-command"}}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"next":3,"results":[{"id":"apr-1","metadata":{"command_id":"cmd-1"}}]}`))
		default:
			t.Error("lookup requested a page after the match")
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	id, err := FindCommandApprovalRequestID(newTestClient(ts), "cmd-1")
	require.NoError(t, err)
	assert.Equal(t, "apr-1", id)
	assert.EqualValues(t, 2, pages.Load())
}
