package approval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func newTestClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}
}

func TestListApprovalRequests(t *testing.T) {
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
		assert.Equal(t, "/api/approvals/", r.URL.Path)
		assert.Equal(t, "pending", r.URL.Query().Get("status"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[ApprovalRequest]{Count: 1, Results: requests})
	}))
	defer ts.Close()

	list, err := ListApprovalRequests(newTestClient(ts), "pending", "")
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "apr-1", list[0].ID)
	assert.Equal(t, "work_session", list[0].Type)
	assert.Equal(t, "alice", list[0].RequestedBy)
}

func TestListApprovalRequests_TypeFilter(t *testing.T) {
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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/approvals/-/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[ApprovalRequest]{Count: 0, Results: nil})
	}))
	defer ts.Close()

	_, err := ListMyApprovalRequests(newTestClient(ts), "pending", "")
	assert.NoError(t, err)
}

func TestGetApprovalRequest(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "apr-abc/"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ApprovalRequest{
			ID: "apr-abc", RequestType: "sudo", Status: "pending", AddedAt: now,
		})
	}))
	defer ts.Close()

	req, err := GetApprovalRequest(newTestClient(ts), "apr-abc")
	assert.NoError(t, err)
	assert.Equal(t, "apr-abc", req.ID)
	assert.Equal(t, "sudo", req.RequestType)
}

func TestApproveRequest_NoAdjustments(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "apr-abc/approve/"))
		var body ApproveOptions
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Nil(t, body.AdjustedScopes)
		assert.Nil(t, body.AdjustedServers)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	err := ApproveRequest(newTestClient(ts), "apr-abc", ApproveOptions{})
	assert.NoError(t, err)
}

func TestApproveRequest_WithAdjustments(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "apr-abc/approve/"))
		var body ApproveOptions
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"command"}, body.AdjustedScopes)
		assert.Equal(t, []string{"srv-uuid-1"}, body.AdjustedServers)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	err := ApproveRequest(newTestClient(ts), "apr-abc", ApproveOptions{
		AdjustedScopes:  []string{"command"},
		AdjustedServers: []string{"srv-uuid-1"},
	})
	assert.NoError(t, err)
}

func TestRejectRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "apr-abc/reject/"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	err := RejectRequest(newTestClient(ts), "apr-abc")
	assert.NoError(t, err)
}
