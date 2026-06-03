package worksession

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

func TestGetWorkSessionList(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	sessions := []WorkSession{
		{
			ID:            "ses-1",
			Description:   "nginx fix",
			Status:        "active",
			RequesterType: "user",
			Scopes:        []string{"command", "websh"},
			Servers:       []types.ServerSummary{{ID: "srv-1", Name: "web-01"}},
			ExpiresAt:     now.Add(2 * time.Hour),
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/work-sessions/sessions/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[WorkSession]{Count: 1, Results: sessions})
	}))
	defer ts.Close()

	list, err := GetWorkSessionList(newTestClient(ts), "", "", "")
	assert.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "ses-1", list[0].ID)
	assert.Equal(t, "active", list[0].Status)
	assert.Equal(t, "command, websh", list[0].Scopes)
	assert.Equal(t, "web-01", list[0].Servers)
}

func TestGetWorkSessionList_StatusFilter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "pending", r.URL.Query().Get("status"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[WorkSession]{Count: 0, Results: nil})
	}))
	defer ts.Close()

	_, err := GetWorkSessionList(newTestClient(ts), "pending", "", "")
	assert.NoError(t, err)
}

func TestGetWorkSessionList_AssignedUserFilter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "6eaa827d-616a-4fa9-ad42-4fbb67bb007b", r.URL.Query().Get("assigned_user"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[WorkSession]{Count: 0, Results: nil})
	}))
	defer ts.Close()

	_, err := GetWorkSessionList(newTestClient(ts), "", "", "6eaa827d-616a-4fa9-ad42-4fbb67bb007b")
	assert.NoError(t, err)
}

func TestCreateWorkSession(t *testing.T) {
	now := time.Now().UTC().Add(time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/work-sessions/sessions/", r.URL.Path)

		var req WorkSessionCreateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "nginx fix", req.Description)
		assert.Equal(t, []string{"command"}, req.Scopes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-new", Status: "pending", ExpiresAt: now})
	}))
	defer ts.Close()

	req := WorkSessionCreateRequest{
		Description:   "nginx fix",
		RequesterType: "user",
		Scopes:        []string{"command"},
		Servers:       []string{"srv-1"},
		ExpiresAt:     now.Format(time.RFC3339),
	}
	session, err := CreateWorkSession(newTestClient(ts), req)
	assert.NoError(t, err)
	assert.Equal(t, "ses-new", session.ID)
	assert.Equal(t, "pending", session.Status)
}

func TestGetWorkSession(t *testing.T) {
	now := time.Now().UTC().Add(time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "approved", ExpiresAt: now})
	}))
	defer ts.Close()

	session, err := GetWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
	assert.Equal(t, "ses-abc", session.ID)
	assert.Equal(t, "approved", session.Status)
}

func TestActivateWorkSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/activate/"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "active"})
	}))
	defer ts.Close()

	err := ActivateWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

func TestCompleteWorkSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/complete/"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "completed"})
	}))
	defer ts.Close()

	err := CompleteWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

func TestExtendWorkSession(t *testing.T) {
	newExpiry := time.Now().UTC().Add(4 * time.Hour).Format(time.RFC3339)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/extend/"))

		var req WorkSessionExtendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, newExpiry, req.ExpiresAt)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "active"})
	}))
	defer ts.Close()

	err := ExtendWorkSession(newTestClient(ts), "ses-abc", WorkSessionExtendRequest{ExpiresAt: newExpiry})
	assert.NoError(t, err)
}

func TestGetWorkSessionList_RequesterTypeFilter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "agent", r.URL.Query().Get("requester_type"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[WorkSession]{Count: 0, Results: nil})
	}))
	defer ts.Close()

	_, err := GetWorkSessionList(newTestClient(ts), "", "agent", "")
	assert.NoError(t, err)
}

func TestGetWorkSessionList_ScopesJoined(t *testing.T) {
	now := time.Now().UTC().Add(time.Hour)
	sessions := []WorkSession{
		{ID: "s1", Scopes: []string{"command", "websh", "webftp"}, ExpiresAt: now},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[WorkSession]{Count: 1, Results: sessions})
	}))
	defer ts.Close()

	list, err := GetWorkSessionList(newTestClient(ts), "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "command, websh, webftp", list[0].Scopes)
}

func TestRejectWorkSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/reject/"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "rejected"})
	}))
	defer ts.Close()

	err := RejectWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

func TestRevokeWorkSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/revoke/"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "revoked"})
	}))
	defer ts.Close()

	err := RevokeWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

func TestApproveWorkSession_NoAdjustments(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/approve/"))

		var req WorkSessionApproveRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Nil(t, req.AdjustedScopes)
		assert.Nil(t, req.AdjustedServers)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "approved"})
	}))
	defer ts.Close()

	err := ApproveWorkSession(newTestClient(ts), "ses-abc", WorkSessionApproveRequest{})
	assert.NoError(t, err)
}

func TestApproveWorkSession_WithAdjustments(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/approve/"))

		var req WorkSessionApproveRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, []string{"command"}, req.AdjustedScopes)
		assert.Equal(t, []string{"srv-uuid-1"}, req.AdjustedServers)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "approved"})
	}))
	defer ts.Close()

	req := WorkSessionApproveRequest{
		AdjustedScopes:  []string{"command"},
		AdjustedServers: []string{"srv-uuid-1"},
	}
	err := ApproveWorkSession(newTestClient(ts), "ses-abc", req)
	assert.NoError(t, err)
}

func TestGetWorkSessionTimeline(t *testing.T) {
	ts := newString("2024-01-15T10:30:00Z")
	items := []TimelineItem{
		{Type: "command", Timestamp: ts, Line: "ls -la"},
		{Type: "websh_session", Timestamp: ts},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-abc/timeline/"))
		assert.Equal(t, "true", r.URL.Query().Get("include_records"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[TimelineItem]{Count: 2, Results: items})
	}))
	defer srv.Close()

	result, err := GetWorkSessionTimeline(newTestClient(srv), "ses-abc", true)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "command", result[0].Type)
	assert.Equal(t, "ls -la", result[0].Line)
}

func TestGetWorkSessionTimeline_ExcludeRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "ses-xyz/timeline/"))
		assert.Equal(t, "false", r.URL.Query().Get("include_records"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[TimelineItem]{Count: 0, Results: nil})
	}))
	defer srv.Close()

	result, err := GetWorkSessionTimeline(newTestClient(srv), "ses-xyz", false)
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestUpdateWorkSession(t *testing.T) {
	var gotBody WorkSessionUpdateRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/work-sessions/sessions/ses-abc/", r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "active"})
	}))
	defer ts.Close()

	req := WorkSessionUpdateRequest{SudoPolicies: []SudoPolicyInline{
		{ID: "pol-1", Commands: []string{"systemctl restart nginx"}, AllowBypassMFA: true},
		{Commands: []string{"tail -f /var/log/nginx/*.log"}, AllowBypassMFA: true},
	}}
	session, err := UpdateWorkSession(newTestClient(ts), "ses-abc", req)
	assert.NoError(t, err)
	assert.Equal(t, "ses-abc", session.ID)
	// Full desired set is sent: existing policy echoed back with its ID
	// plus the new addition without one.
	assert.Len(t, gotBody.SudoPolicies, 2)
	assert.Equal(t, "pol-1", gotBody.SudoPolicies[0].ID)
	assert.Empty(t, gotBody.SudoPolicies[1].ID)
}

func newString(s string) *string { return &s }
