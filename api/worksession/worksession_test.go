package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(ts *httptest.Server) *client.AlpaconClient {
	return &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}
}

func TestGetWorkSessionList(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "ses-1", list[0].ID)
	assert.Equal(t, "active", list[0].Status)
	assert.Equal(t, "command, websh", list[0].Scopes)
	assert.Equal(t, "web-01", list[0].Servers)
}

func TestGetWorkSessionList_StatusFilter(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	require.NoError(t, err)
	assert.Equal(t, "ses-new", session.ID)
	assert.Equal(t, "pending", session.Status)
}

func TestGetWorkSession(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Add(time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/work-sessions/sessions/ses-abc/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "approved", ExpiresAt: now})
	}))
	defer ts.Close()

	session, err := GetWorkSession(newTestClient(ts), "ses-abc")
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", session.ID)
	assert.Equal(t, "approved", session.Status)
}

func TestActivateWorkSession(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/work-sessions/sessions/ses-abc/activate/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "active"})
	}))
	defer ts.Close()

	err := ActivateWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

func TestCompleteWorkSession(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/work-sessions/sessions/ses-abc/complete/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "completed"})
	}))
	defer ts.Close()

	err := CompleteWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

func TestExtendWorkSession(t *testing.T) {
	t.Parallel()
	newExpiry := time.Now().UTC().Add(4 * time.Hour).Format(time.RFC3339)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/work-sessions/sessions/ses-abc/extend/", r.URL.Path)

		var req WorkSessionExtendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, newExpiry, req.ExpiresAt)
		assert.Empty(t, req.Reason)

		w.Header().Set("Content-Type", "application/json")
		expiresAt, _ := time.Parse(time.RFC3339, newExpiry)
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "active", ExpiresAt: expiresAt})
	}))
	defer ts.Close()

	session, status, err := ExtendWorkSession(newTestClient(ts), "ses-abc", WorkSessionExtendRequest{ExpiresAt: newExpiry})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ses-abc", session.ID)
	assert.Nil(t, session.PendingExtensionRequest)
}

// TestExtendWorkSession_ReasonSent covers the wire shape the extend action
// expects: reason is required on every request, auto-approved or queued
// alike, and the CLI sends --reason as 'reason' on the same request body, not
// a separate call.
func TestExtendWorkSession_ReasonSent(t *testing.T) {
	t.Parallel()
	newExpiry := time.Now().UTC().Add(4 * time.Hour).Format(time.RFC3339)
	var gotBody WorkSessionExtendRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "active"})
	}))
	defer ts.Close()

	_, _, err := ExtendWorkSession(newTestClient(ts), "ses-abc", WorkSessionExtendRequest{ExpiresAt: newExpiry, Reason: "customer escalation"})
	require.NoError(t, err)
	assert.Equal(t, "customer escalation", gotBody.Reason)
}

// TestExtendWorkSession_PendingApproval covers the 202 shape: same WorkSession
// body, with PendingExtensionRequest populated instead of ExpiresAt already
// advanced, and the 202 status returned alongside it—the caller distinguishes
// 200 from 202 by the status, so it is asserted explicitly here too.
func TestExtendWorkSession_PendingApproval(t *testing.T) {
	t.Parallel()
	addedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	requested := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	requestDeadline := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	currentExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(WorkSession{
			ID:        "ses-abc",
			Status:    "active",
			ExpiresAt: currentExpiry,
			PendingExtensionRequest: &PendingExtensionRequest{
				ID:                 "apr-1",
				RequestedExpiresAt: requested,
				ExpiresAt:          requestDeadline,
				Reason:             "customer escalation",
				AddedAt:            addedAt,
			},
		})
	}))
	defer ts.Close()

	session, status, err := ExtendWorkSession(newTestClient(ts), "ses-abc", WorkSessionExtendRequest{
		ExpiresAt: requested.Format(time.RFC3339),
		Reason:    "customer escalation",
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, status)
	require.NotNil(t, session.PendingExtensionRequest)
	assert.Equal(t, "apr-1", session.PendingExtensionRequest.ID)
	assert.True(t, requested.Equal(session.PendingExtensionRequest.RequestedExpiresAt))
	assert.True(t, requestDeadline.Equal(session.PendingExtensionRequest.ExpiresAt))
	assert.Equal(t, "customer escalation", session.PendingExtensionRequest.Reason)
	assert.True(t, addedAt.Equal(session.PendingExtensionRequest.AddedAt))
	// The session's own expiry is unchanged—the extension has not applied yet.
	assert.True(t, currentExpiry.Equal(session.ExpiresAt))
}

func TestGetWorkSessionList_RequesterTypeFilter(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	require.NoError(t, err)
	assert.Equal(t, "command, websh, webftp", list[0].Scopes)
}

func TestRevokeWorkSession(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/work-sessions/sessions/ses-abc/revoke/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "revoked"})
	}))
	defer ts.Close()

	err := RevokeWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

func TestCancelWorkSession(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/work-sessions/sessions/ses-abc/cancel/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WorkSession{ID: "ses-abc", Status: "cancelled"})
	}))
	defer ts.Close()

	err := CancelWorkSession(newTestClient(ts), "ses-abc")
	assert.NoError(t, err)
}

// timelineSessionID is the session every timeline contract server answers for,
// so the handler can pin the whole path instead of its tail.
const timelineSessionID = "ses-abc"

// newTimelineContractServer serves the timeline route's two response shapes.
// Naming `cursor` or `page_size` selects the paginated one, whose `next` is an
// opaque string and whose pages carry no recordings whatever `include_records`
// asked for; naming neither serves the whole timeline under `results`, with the
// recordings embedded when `include_records` is not false. A read that opts
// into pagination without meaning to therefore loses its recordings here, the
// way it does against the server.
func newTimelineContractServer(t *testing.T, items []TimelineItem, pageSize int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/work-sessions/sessions/"+timelineSessionID+"/timeline/", r.URL.Path)
		query := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")

		if query.Get("cursor") == "" && query.Get("page_size") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": filterTimelineRecords(items, query.Get("include_records") != "false"),
			})
			return
		}

		paged := filterTimelineRecords(items, false)
		start := 0
		if cursor := query.Get("cursor"); cursor != "" {
			parsed, err := strconv.Atoi(cursor)
			if err != nil || parsed < 0 || parsed > len(paged) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code": "api_invalid_cursor"}`))
				return
			}
			start = parsed
		}
		end := min(start+pageSize, len(paged))
		next := ""
		if end < len(paged) {
			next = strconv.Itoa(end)
		}
		_ = json.NewEncoder(w).Encode(api.CursorListResponse[TimelineItem]{Next: next, Results: paged[start:end]})
	}))
}

func filterTimelineRecords(items []TimelineItem, keepRecords bool) []TimelineItem {
	out := make([]TimelineItem, 0, len(items))
	for _, item := range items {
		if item.Type == "websh_record" && !keepRecords {
			continue
		}
		out = append(out, item)
	}
	return out
}

func timelineFixture() []TimelineItem {
	ts := newString("2024-01-15T10:30:00Z")
	return []TimelineItem{
		{Type: "command", Timestamp: ts, Line: "ls -la"},
		{Type: "websh_session", Timestamp: ts, ID: "wsh-1"},
		{Type: "command", Timestamp: ts, Line: "systemctl status nginx"},
		{Type: "websh_record", Timestamp: ts, SessionID: "wsh-1", MaskedRecord: "ls -la\n"},
	}
}

func TestGetWorkSessionTimeline(t *testing.T) {
	t.Parallel()
	srv := newTimelineContractServer(t, timelineFixture(), 2)
	defer srv.Close()

	result, err := GetWorkSessionTimeline(newTestClient(srv), timelineSessionID, true)
	require.NoError(t, err)
	require.Len(t, result, 4)
	assert.Equal(t, "command", result[0].Type)
	assert.Equal(t, "ls -la", result[0].Line)
}

// The recordings a read asked for only exist in the unpaginated shape, so a
// request that names page_size or cursor reports a session with recordings as
// having none—which is what 'work-session recording' prints.
func TestGetWorkSessionTimeline_WithRecordsKeepsTheRecordings(t *testing.T) {
	t.Parallel()
	srv := newTimelineContractServer(t, timelineFixture(), 2)
	defer srv.Close()

	result, err := GetWorkSessionTimeline(newTestClient(srv), timelineSessionID, true)
	require.NoError(t, err)

	var records []TimelineItem
	for _, item := range result {
		if item.Type == "websh_record" {
			records = append(records, item)
		}
	}
	require.Len(t, records, 1)
	assert.Equal(t, "wsh-1", records[0].SessionID)
	assert.Equal(t, "ls -la\n", records[0].MaskedRecord)
}

// The paginated shape answers `next` as an opaque string, and its pages have to
// be followed to the end rather than read as one page-number walk's first page.
func TestGetWorkSessionTimeline_ExcludeRecordsFollowsTheCursor(t *testing.T) {
	t.Parallel()
	srv := newTimelineContractServer(t, timelineFixture(), 1)
	defer srv.Close()

	result, err := GetWorkSessionTimeline(newTestClient(srv), timelineSessionID, false)
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, "ls -la", result[0].Line)
	assert.Equal(t, "wsh-1", result[1].ID)
	assert.Equal(t, "systemctl status nginx", result[2].Line)
	for _, item := range result {
		assert.NotEqual(t, "websh_record", item.Type)
	}
}

// A server that does not paginate this route answers the whole list whatever
// the request names, so include_records is the only thing keeping recordings
// out of a read that asked for none. It rides on the paginated request for
// that reason alone.
func TestGetWorkSessionTimeline_ExcludeRecordsOnAnUnpaginatedServer(t *testing.T) {
	t.Parallel()
	items := timelineFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/work-sessions/sessions/"+timelineSessionID+"/timeline/", r.URL.Path)
		keepRecords := r.URL.Query().Get("include_records") != "false"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": filterTimelineRecords(items, keepRecords)})
	}))
	defer srv.Close()

	result, err := GetWorkSessionTimeline(newTestClient(srv), timelineSessionID, false)
	require.NoError(t, err)
	require.Len(t, result, 3)
	for _, item := range result {
		assert.NotEqual(t, "websh_record", item.Type)
	}
}

func TestUpdateWorkSession(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)
	assert.Equal(t, "ses-abc", session.ID)
	// Full desired set is sent: existing policy echoed back with its ID
	// plus the new addition without one.
	assert.Len(t, gotBody.SudoPolicies, 2)
	assert.Equal(t, "pol-1", gotBody.SudoPolicies[0].ID)
	assert.Empty(t, gotBody.SudoPolicies[1].ID)
}

func newString(s string) *string { return &s }

func TestWorkSessionUnmarshalAdjustmentsAndRecommendations(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "ses-1",
		"status": "approved",
		"adjustments": {
			"scopes": {"old": ["command", "websh"], "new": ["command"]},
			"servers": {
				"old": [{"id": "srv-1", "name": "web-01"}, {"id": "srv-2", "name": "db-01"}],
				"new": [{"id": "srv-1", "name": "web-01"}]
			}
		},
		"recommendations": [
			{"id": "r1", "text": "Rotate the key", "severity": "high", "source": "admin_added", "auto_checkable": false},
			{"id": "r2", "text": "Prefer reload", "severity": "low", "source": "ai_suggested", "auto_checkable": true}
		]
	}`)

	var ws WorkSession
	require.NoError(t, json.Unmarshal(body, &ws))

	assert.NotNil(t, ws.Adjustments)
	assert.Equal(t, []string{"command", "websh"}, ws.Adjustments.Scopes.Old)
	assert.Equal(t, []string{"command"}, ws.Adjustments.Scopes.New)
	assert.Equal(t, "web-01", ws.Adjustments.Servers.Old[0].Name)
	assert.Len(t, ws.Adjustments.Servers.New, 1)

	assert.Len(t, ws.Recommendations, 2)
	assert.Equal(t, "high", ws.Recommendations[0].Severity)
	assert.Equal(t, "Rotate the key", ws.Recommendations[0].Text)
	assert.True(t, ws.Recommendations[1].AutoCheckable)
}

func TestWorkSessionUnmarshalNoAdjustments(t *testing.T) {
	t.Parallel()
	var ws WorkSession
	require.NoError(t, json.Unmarshal([]byte(`{"id":"ses-1","adjustments":null,"recommendations":[]}`), &ws))
	assert.Nil(t, ws.Adjustments)
	assert.Empty(t, ws.Recommendations)
}
