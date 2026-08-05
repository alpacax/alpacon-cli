package websh

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSessionList(t *testing.T) {
	closedTime := "2026-03-01T00:00:00Z"

	sessions := []SessionDetailResponse{
		{
			ID:       "sess-1",
			Server:   types.ServerSummary{Name: "web-server"},
			User:     types.UserSummary{Name: "alice"},
			Username: "alice",
			RemoteIP: "10.0.0.1",
			AddedAt:  "2026-03-01T00:00:00Z",
			ClosedAt: nil,
		},
		{
			ID:       "sess-2",
			Server:   types.ServerSummary{Name: "db-server"},
			User:     types.UserSummary{Name: "bob"},
			Username: "bob",
			RemoteIP: "10.0.0.2",
			AddedAt:  "2026-03-02T00:00:00Z",
			ClosedAt: &closedTime,
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "true", r.URL.Query().Get("is_connectable"))

		resp := api.ListResponse[SessionDetailResponse]{
			Count:   len(sessions),
			Results: sessions,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	list, err := GetSessionList(ac)
	require.NoError(t, err)

	assert.Len(t, list, 2)

	assert.Equal(t, "sess-1", list[0].ID)
	assert.Equal(t, "web-server", list[0].Server)
	assert.Equal(t, "alice", list[0].User)
	assert.Equal(t, "-", list[0].ClosedAt)

	assert.Equal(t, "sess-2", list[1].ID)
	assert.Equal(t, "db-server", list[1].Server)
	assert.Equal(t, closedTime, list[1].ClosedAt)
}

func TestGetSessionDetail(t *testing.T) {
	detail := SessionDetailResponse{
		ID:       "sess-abc",
		Server:   types.ServerSummary{Name: "test-server"},
		User:     types.UserSummary{Name: "admin"},
		Username: "admin",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.True(t, strings.Contains(r.URL.Path, "sess-abc"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detail)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	body, err := GetSessionDetail(ac, "sess-abc")
	require.NoError(t, err)

	var got SessionDetailResponse
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "sess-abc", got.ID)
	assert.Equal(t, "test-server", got.Server.Name)
}

func TestCloseSession(t *testing.T) {
	var called bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "sess-123/close")
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	err := CloseSession(ac, "sess-123")
	require.NoError(t, err)
	assert.True(t, called)
}

func TestForceCloseSession(t *testing.T) {
	var called bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "sess-123/force-close")
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	err := ForceCloseSession(ac, "sess-123")
	require.NoError(t, err)
	assert.True(t, called)
}

func TestConnectToSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		var req ConnectRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "sess-xyz", req.Session)
		assert.False(t, req.IsMaster)
		assert.True(t, req.ReadOnly)

		resp := SessionResponse{ID: "channel-1", WebsocketURL: "ws://localhost/ws"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := ConnectToSession(ac, "sess-xyz")
	require.NoError(t, err)
	assert.Equal(t, "channel-1", resp.ID)
}

func TestInviteToSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.Contains(r.URL.Path, "sess-abc/invite"))

		var req InviteRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, []string{"a@example.com", "b@example.com"}, req.Emails)
		assert.True(t, req.ReadOnly)

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	err := InviteToSession(ac, "sess-abc", []string{"a@example.com", "b@example.com"}, true)
	require.NoError(t, err)
}

func TestJoinWebshSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.Contains(r.URL.Path, "chan-id-123/join"))

		var req JoinRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "secret", req.Password)

		resp := SessionResponse{ID: "joined-session", WebsocketURL: "ws://localhost/ws"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := JoinWebshSession(ac, "https://example.com/websh/shared/abc?channel=chan-id-123", "secret")
	require.NoError(t, err)
	assert.Equal(t, "joined-session", resp.ID)
}

func TestJoinWebshSession_InvalidURL(t *testing.T) {
	ac := &client.AlpaconClient{}
	_, err := JoinWebshSession(ac, "https://example.com/no-channel-param", "password")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid URL format")
}

func TestBuildSessionRequest_OmitsEmptyWorkSession(t *testing.T) {
	req := BuildSessionRequest("srv-1", "alice", "ops", 24, 80, "")
	assert.Empty(t, req.WorkSession)
	assert.Equal(t, "srv-1", req.Server)
	assert.Equal(t, "alice", req.Username)
	assert.Equal(t, "ops", req.Groupname)
	assert.Equal(t, 24, req.Rows)
	assert.Equal(t, 80, req.Cols)

	// Verify omitempty: the JSON wire form must not include "work_session".
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "work_session")
}

func TestBuildSessionRequest_IncludesWorkSession(t *testing.T) {
	req := BuildSessionRequest("srv-1", "", "", 24, 80, "ses-abc")
	assert.Equal(t, "ses-abc", req.WorkSession)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"work_session":"ses-abc"`)
}

func TestGetSessionRecords_FollowsCursor(t *testing.T) {
	var gotCursors, gotPageSizes []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/records/"))
		gotCursors = append(gotCursors, r.URL.Query().Get("cursor"))
		gotPageSizes = append(gotPageSizes, r.URL.Query().Get("page_size"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(api.CursorListResponse[SessionRecord]{
				Next:    "TOKEN2",
				Results: []SessionRecord{{AddedAt: "t1", Record: "docker ps"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.CursorListResponse[SessionRecord]{
			Results: []SessionRecord{{AddedAt: "t2", Record: "ls -la"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	records, err := GetSessionRecords(ac, "sess-1", "", 5)
	require.NoError(t, err)

	require.Len(t, records, 2)
	assert.Equal(t, []string{"", "TOKEN2"}, gotCursors)
	// page_size derives from limit and the remaining count, proving limit is wired through.
	assert.Equal(t, []string{"5", "4"}, gotPageSizes)
	assert.Equal(t, "ls -la", records[1].Record)
}

func TestGetSessionRecords_QueryHitsSearchEndpoint(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/search/"))
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.CursorListResponse[SessionRecord]{
			Results: []SessionRecord{{Record: "docker ps -a"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	records, err := GetSessionRecords(ac, "sess-1", "docker", 100)
	require.NoError(t, err)
	assert.Equal(t, "docker", gotQuery)
	assert.Len(t, records, 1)
}

func TestRunWsClientReportsTerminalSetupFailure(t *testing.T) {
	helper := exec.Command(os.Args[0], "-test.run=TestRunWsClientTerminalHelperProcess")
	helper.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	var stderr bytes.Buffer
	helper.Stderr = &stderr
	require.Error(t, helper.Run())

	assert.Contains(t, stderr.String(), "failed to set up terminal: websh command should be a terminal")
	assert.NotContains(t, stderr.String(), "websocket connection failed")
}

func TestRunWsClientTerminalHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	// stdin is not a tty here, so checkTerminal fails before the connection is used.
	wsClient := &WebsocketClient{Done: make(chan error, 1)}
	_ = wsClient.runWsClient()
}
