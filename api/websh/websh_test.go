package websh

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// A blocked goroutine never returns, so any bound proves the point.
	teardownWait = 2 * time.Second

	receivedBufferSize = 8
)

// The page walk itself is pinned in api.TestFetchPagesUpTo_*; what is specific to
// GetSessionList is that tail reaches the helper as its limit, so a tail larger than the
// server's 100-item page cap still yields exactly tail sessions.
func TestGetSessionList_PassesTailAsTheLimit(t *testing.T) {
	t.Parallel()
	// Twice the pages the tail needs, so a walk that ignored the limit comes back with 500
	// and fails the length assertion instead of running until the test timeout.
	const lastPage = 5

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		size, sizeErr := strconv.Atoi(r.URL.Query().Get("page_size"))
		page, pageErr := strconv.Atoi(r.URL.Query().Get("page"))
		if sizeErr != nil || pageErr != nil {
			t.Errorf("page and page_size must be integers, got page=%q page_size=%q",
				r.URL.Query().Get("page"), r.URL.Query().Get("page_size"))
			http.Error(w, "bad pagination query", http.StatusBadRequest)
			return
		}

		next := page + 1
		if page >= lastPage {
			next = 0
		}
		results := make([]SessionDetailResponse, size)
		_ = json.NewEncoder(w).Encode(api.ListResponse[SessionDetailResponse]{Next: next, Results: results})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	list, err := GetSessionList(ac, 250)

	require.NoError(t, err)
	assert.Len(t, list, 250)
}

func TestGetSessionList(t *testing.T) {
	t.Parallel()
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
	list, err := GetSessionList(ac, 25)
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
	t.Parallel()
	detail := SessionDetailResponse{
		ID:       "sess-abc",
		Server:   types.ServerSummary{Name: "test-server"},
		User:     types.UserSummary{Name: "admin"},
		Username: "admin",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "sess-abc")

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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		var req ConnectRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
			return
		}
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
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "sess-abc/invite")

		var req InviteRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
			return
		}
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
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "chan-id-123/join")

		var req JoinRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
			return
		}
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
	t.Parallel()
	ac := &client.AlpaconClient{}
	_, err := JoinWebshSession(ac, "https://example.com/no-channel-param", "password")
	require.ErrorContains(t, err, "invalid URL format")
}

func TestBuildSessionRequest_OmitsEmptyWorkSession(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	req := BuildSessionRequest("srv-1", "", "", 24, 80, "ses-abc")
	assert.Equal(t, "ses-abc", req.WorkSession)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"work_session":"ses-abc"`)
}

func TestGetSessionRecords_FollowsCursor(t *testing.T) {
	t.Parallel()
	var gotCursors, gotPageSizes []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/websh/sessions/sess-1/records/", r.URL.Path)
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
	t.Parallel()
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/websh/sessions/sess-1/search/", r.URL.Path)
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

// Each of these can still be running once the outcome is taken, and each has to
// notice and leave. readFromServer is absent: it ends on its own failing read,
// covered separately.
func TestSessionGoroutines_ReturnAfterTheOutcomeIsTaken(t *testing.T) {
	tests := []struct {
		name  string
		start func(t *testing.T, wsClient *WebsocketClient) func()
	}{
		{
			name: "readUserInput reporting EOF",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
				t.Helper()
				pipeWrite := pipeStdin(t)
				require.NoError(t, pipeWrite.Close()) // stdin yields EOF right away

				return func() { wsClient.readUserInput(make(chan string, 1)) }
			},
		},
		{
			name: "readUserInput sending to inputChan",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
				t.Helper()
				pipeWrite := pipeStdin(t)
				_, err := pipeWrite.WriteString("x")
				require.NoError(t, err)

				inputChan := make(chan string, 1)
				inputChan <- "buffered" // writeToServer has returned, so nothing drains this

				return func() { wsClient.readUserInput(inputChan) }
			},
		},
		{
			// The writer reports to its connection rather than to the session, since
			// a connection ending is what a reconnect recovers from.
			name: "writeToServer waiting to flush",
			start: func(t *testing.T, _ *WebsocketClient) func() {
				t.Helper()
				conn := newConnection(nil)
				conn.finish(errors.New("the connection dropped"))

				// Empty, so only the ended branch can end the loop: nothing is ever flushed.
				return func() { conn.writeToServer(make(chan string)) }
			},
		},
		{
			name: "watchInterrupt waiting for a signal",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
				t.Helper()
				// No signal ever arrives, so only the done branch can release the watcher.
				return func() { wsClient.watchInterrupt(make(chan os.Signal)) }
			},
		},
		{
			name: "readCtrlC waiting for the next byte",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
				t.Helper()
				pipeWrite := pipeStdin(t)
				_, err := pipeWrite.WriteString("x") // not Ctrl+C, so the loop goes around
				require.NoError(t, err)

				return func() { wsClient.readCtrlC() }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsClient := newWebsocketClient(nil)
			wsClient.finish(errors.New("teardown already reported"))

			awaitReturn(t, tt.name+" parked after the outcome was taken", tt.start(t, wsClient))
		})
	}
}

func TestReadUserInput_ReportsEOFAsACleanEnd(t *testing.T) {
	pipeWrite := pipeStdin(t)
	require.NoError(t, pipeWrite.Close()) // what Ctrl+D leaves behind

	wsClient := newWebsocketClient(nil)

	awaitReturn(t, "readUserInput parked on EOF", func() {
		wsClient.readUserInput(make(chan string, 1))
	})

	assertReported(t, wsClient)
	assert.NoError(t, wsClient.err) // Ctrl+D is how a session is meant to end
}

func TestReadCtrlC_EndsTheSessionOnCtrlC(t *testing.T) {
	pipeWrite := pipeStdin(t)
	// The leading byte proves the loop goes around rather than ending on any input.
	_, err := pipeWrite.Write([]byte{'a', ctrlC})
	require.NoError(t, err)

	wsClient := newWebsocketClient(nil)

	awaitReturn(t, "readCtrlC parked on the Ctrl+C byte", wsClient.readCtrlC)

	assertReported(t, wsClient)
	assert.NoError(t, wsClient.err) // Ctrl+C is how a session is meant to end
}

func TestWriteToServer_ReportsWriteFailure(t *testing.T) {
	t.Parallel()
	ws, _ := dialTestServer(t)
	require.NoError(t, ws.Close()) // every later WriteMessage fails
	conn := newConnection(ws)

	inputChan := make(chan string, 1)
	inputChan <- "x" // buffered input forces the failing write

	awaitReturn(t, "writeToServer parked on the failing write", func() {
		conn.writeToServer(inputChan)
	})

	assertEnded(t, conn)
	assert.Error(t, conn.err)
}

func TestReadFromServer_ReportsReadFailure(t *testing.T) {
	t.Parallel()
	ws, _ := dialTestServer(t)
	require.NoError(t, ws.Close()) // every later ReadMessage fails

	conn := newConnection(ws)

	awaitReturn(t, "readFromServer parked on the failing read", func() {
		conn.readFromServer()
	})

	assertEnded(t, conn)
	assert.Error(t, conn.err)
}

func TestReadFromServer_PrintsEveryMessage(t *testing.T) {
	ws, _ := dialTestServer(t, "hi ", "there")
	conn := newConnection(ws)

	stdout := testutil.CaptureStdout(t, func() {
		awaitReturn(t, "readFromServer parked after the server went away", func() {
			conn.readFromServer()
		})
	})

	assert.Equal(t, "hi there", stdout) // the loop must survive a successful read
	assertEnded(t, conn)
	assert.Error(t, conn.err)
}

func TestReadUserInput_ForwardsToTheWriter(t *testing.T) {
	pipeWrite := pipeStdin(t)
	_, err := pipeWrite.WriteString("l")
	require.NoError(t, err)

	wsClient := newWebsocketClient(nil)
	t.Cleanup(func() { wsClient.finish(nil) })

	inputChan := make(chan string, 1)
	go wsClient.readUserInput(inputChan)

	select {
	case got := <-inputChan:
		assert.Equal(t, "l", got)
	case <-time.After(teardownWait):
		require.Fail(t, "readUserInput never forwarded the rune it read")
	}
}

func TestWriteToServer_FlushesBufferedInput(t *testing.T) {
	t.Parallel()
	ws, received := dialTestServer(t)
	conn := newConnection(ws)
	t.Cleanup(func() { conn.finish(nil) })

	inputChan := make(chan string, 2)
	inputChan <- "l"
	inputChan <- "s"
	go conn.writeToServer(inputChan)

	// The two runes may land in one flush or two, but every byte has to arrive.
	var got string
	deadline := time.After(teardownWait)
	for got != "ls" {
		select {
		case message := <-received:
			got += message
		case <-deadline:
			require.Fail(t, "writeToServer never flushed the buffered input", "received %q", got)
		}
	}
}

func TestWatchInterrupt_EndsTheSessionOnSignal(t *testing.T) {
	t.Parallel()
	wsClient := newWebsocketClient(nil)

	sigChan := make(chan os.Signal, 1)
	sigChan <- os.Interrupt

	awaitReturn(t, "watchInterrupt parked on the signal", func() {
		wsClient.watchInterrupt(sigChan)
	})

	assertReported(t, wsClient)
	assert.NoError(t, wsClient.err) // Ctrl+C is how a session is meant to end
}

func TestFinish_KeepsTheFirstOutcome(t *testing.T) {
	t.Parallel()
	wsClient := newWebsocketClient(nil)

	first := errors.New("remote closed the session")
	wsClient.finish(first)
	wsClient.finish(errors.New("write failed on the closed connection"))

	assertReported(t, wsClient)
	assert.Equal(t, first, wsClient.err)
}

func TestFinish_NormalizesARemoteCloseToASuccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		reported  error
		keepAsErr bool
	}{
		{
			// What the proxy actually sends: every other case here is a contract
			// the CLI honors but never meets in production.
			name:     "session end",
			reported: &websocket.CloseError{Code: sessionEndCloseCode},
		},
		{
			name:     "normal closure",
			reported: &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "session ended"},
		},
		{
			name:     "going away",
			reported: &websocket.CloseError{Code: websocket.CloseGoingAway},
		},
		{
			name:      "abnormal closure",
			reported:  &websocket.CloseError{Code: websocket.CloseAbnormalClosure},
			keepAsErr: true,
		},
		{
			name:      "transport failure",
			reported:  errors.New("connection reset by peer"),
			keepAsErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsClient := newWebsocketClient(nil)
			wsClient.finish(tt.reported)

			assertReported(t, wsClient)
			if tt.keepAsErr {
				assert.Equal(t, tt.reported, wsClient.err)
				return
			}
			// Typing exit is not a failure, and every caller exits non-zero on one.
			assert.NoError(t, wsClient.err)
		})
	}
}

// The dial succeeds and raw mode then fails, and nothing on that return calls
// finish. Today every go statement sits below enterRawMode, so none has started
// and the count holds; this fails if one is ever moved back above it.
func TestOpenReadOnlyTerminal_LeavesNoGoroutineWhenSetupFails(t *testing.T) {
	// SetWebsocketHeader sends an Origin, which the default CheckOrigin rejects
	// as cross-origin against the httptest host.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		_ = conn.Close() // the handler must not outlive the assertion below
	}))
	t.Cleanup(ts.Close)

	pipeStdin(t) // a pipe is not a tty, so raw mode fails after the dial succeeds

	// signal.Notify starts one process-wide goroutine on first use anywhere, and
	// it never exits. Priming it keeps that out of the baseline below.
	warmUp := make(chan os.Signal, 1)
	signal.Notify(warmUp, syscall.SIGTERM)
	signal.Stop(warmUp)

	before := runtime.NumGoroutine()
	err := OpenReadOnlyTerminal(&client.AlpaconClient{}, SessionResponse{
		WebsocketURL: "ws" + strings.TrimPrefix(ts.URL, "http"),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdin is not a terminal")

	// Polled inline rather than with assert.Eventually, which runs its condition
	// in a goroutine of its own and so can never see the count come back down.
	deadline := time.Now().Add(teardownWait)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	assert.LessOrEqual(t, runtime.NumGoroutine(), before,
		"a goroutine was started before enterRawMode and outlived OpenReadOnlyTerminal: on this return nothing closes done, and signal.Stop disarms sigChan without closing it, so the watcher would have no way out")
}

func TestRunWsClient_ReportsTerminalSetupFailure(t *testing.T) {
	pipeStdin(t) // a pipe is not a tty, so raw mode cannot be entered

	err := newWebsocketClient(nil).runWsClient()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdin is not a terminal")
	assert.NotContains(t, err.Error(), "websocket connection failed")
}

func TestDial_ReportsTheHandshakeStatus(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)

	err := newWebsocketClient(nil).dial("ws" + strings.TrimPrefix(ts.URL, "http"))

	// gorilla reports every rejected upgrade as "bad handshake", so only the
	// status tells the user which one this was.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "websocket connection failed:")
	assert.Contains(t, err.Error(), "(status 401 Unauthorized)")
}

// dialTestServer returns a live connection, so closing it produces genuine write
// failures, along with the messages the server received. The server writes send and
// then goes away, which is how a real session ends.
func dialTestServer(t *testing.T, send ...string) (*websocket.Conn, <-chan string) {
	t.Helper()

	received := make(chan string, receivedBufferSize)
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer func() { _ = conn.Close() }()
		for _, message := range send {
			if err := conn.WriteMessage(websocket.BinaryMessage, []byte(message)); err != nil {
				return
			}
		}
		if len(send) > 0 {
			return
		}
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			select {
			case received <- string(message):
			default: // an unread message must not park the server past the test
			}
		}
	}))
	t.Cleanup(ts.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn, received
}

// pipeStdin points os.Stdin at a pipe and returns its write end. Closing that end
// yields EOF, which is how a real session ends its input.
func pipeStdin(t *testing.T) *os.File {
	t.Helper()

	pipeRead, pipeWrite, err := os.Pipe()
	require.NoError(t, err)

	realStdin := os.Stdin
	os.Stdin = pipeRead
	t.Cleanup(func() {
		os.Stdin = realStdin
		_ = pipeRead.Close()
		_ = pipeWrite.Close()
	})

	return pipeWrite
}

func assertEnded(t *testing.T, conn *connection) {
	t.Helper()

	select {
	case <-conn.ended:
	default:
		require.Fail(t, "ended must be closed so the connection's other pump can leave")
	}
}

func assertReported(t *testing.T, wsClient *WebsocketClient) {
	t.Helper()

	select {
	case <-wsClient.done:
	default:
		require.Fail(t, "done must be closed so the remaining goroutines can leave")
	}
}

func awaitReturn(t *testing.T, msg string, fn func()) {
	t.Helper()

	returned := make(chan struct{})
	go func() {
		fn()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(teardownWait):
		require.Fail(t, msg)
	}
}

func TestReconnectToSession_AsksForAnInteractiveChannel(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		var req ConnectRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
			return
		}
		assert.Equal(t, "sess-xyz", req.Session)
		// What separates reconnecting to your own session from watching someone
		// else's: a read-only channel would come back to a shell that ignores
		// every keystroke.
		assert.True(t, req.IsMaster)
		assert.False(t, req.ReadOnly)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SessionResponse{ID: "channel-2", WebsocketURL: "ws://localhost/ws"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := ReconnectToSession(ac, "sess-xyz")
	require.NoError(t, err)
	assert.Equal(t, "ws://localhost/ws", resp.WebsocketURL)
}

// Which endings close the shell and which are a link to re-dial. Everything the
// server does deliberately is in the first group; everything else leaves a session
// still running on the far side.
func TestEndsSession_SeparatesAnEndFromADrop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			// What the proxy actually sends at the end of a session.
			name: "session end",
			err:  &websocket.CloseError{Code: sessionEndCloseCode},
			want: true,
		},
		{
			name: "normal closure",
			err:  &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "session ended"},
			want: true,
		},
		{
			name: "going away",
			err:  &websocket.CloseError{Code: websocket.CloseGoingAway},
			want: true,
		},
		{
			name: "no error at all",
			want: true,
		},
		{
			name: "service restart",
			err:  &websocket.CloseError{Code: websocket.CloseServiceRestart},
		},
		{
			name: "abnormal closure",
			err:  &websocket.CloseError{Code: websocket.CloseAbnormalClosure},
		},
		{
			name: "internal server error",
			err:  &websocket.CloseError{Code: websocket.CloseInternalServerErr},
		},
		{
			// A link that went away without a close frame of any kind.
			name: "transport failure",
			err:  errors.New("connection reset by peer"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, endsSession(tt.err))
		})
	}
}

func TestReconnectDelay_DoublesUpToTheCap(t *testing.T) {
	t.Parallel()
	// The whole production schedule, which is what bounds how long a dropped
	// session waits before it is given up.
	var schedule []time.Duration
	for failures := range maxReconnectAttempts {
		schedule = append(schedule, reconnectDelay(failures, reconnectBaseDelay))
	}

	assert.Equal(t, []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		15 * time.Second, // 16s doubled past the cap
	}, schedule)
}

func TestReconnectDelay_CapsABaseAlreadyOverTheLimit(t *testing.T) {
	t.Parallel()
	assert.Equal(t, reconnectMaxDelay, reconnectDelay(0, time.Minute))
	assert.Equal(t, reconnectMaxDelay, reconnectDelay(3, time.Minute))
}

// The whole recovery, end to end: the server closes the first connection with a
// service restart, the client provisions a new channel, dials it, and re-sends the
// terminal size on it.
func TestServeConnections_ReconnectsAfterAServiceRestart(t *testing.T) {
	pinTerminalSize(t, 40, 120)

	var upgrades atomic.Int32
	dialHeaders := make(chan http.Header, receivedBufferSize)
	channels := make(chan ConnectRequest, receivedBufferSize)
	resizes := make(chan SessionSizeRequest, receivedBufferSize)

	// SetWebsocketHeader sends an Origin, which the default CheckOrigin rejects
	// as cross-origin against the httptest host.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case dialHeaders <- r.Header.Clone():
		default:
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer func() { _ = ws.Close() }()

		if upgrades.Add(1) == 1 {
			// A service going down for a restart is not the session ending.
			_ = ws.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseServiceRestart, ""),
				time.Now().Add(teardownWait),
			)
		}
		// Held open until the client goes away: the first connection has a close
		// frame to deliver, and the second is the one the reconnect landed on.
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	mux.HandleFunc(userChannelsBaseURL, func(w http.ResponseWriter, r *http.Request) {
		var req ConnectRequest
		if assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
			select {
			case channels <- req:
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SessionResponse{
			ID:           "channel-2",
			WebsocketURL: "ws://" + r.Host + "/ws/",
		})
	})
	mux.HandleFunc(sessionsBaseURL+"sess-1/", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		var size SessionSizeRequest
		if assert.NoError(t, json.NewDecoder(r.Body).Decode(&size)) {
			select {
			case resizes <- size:
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, UserAgent: "alpacon-cli/test"}
	wsClient := newWebsocketClient(ac.SetWebsocketHeaderWithCapabilities(client.CapabilityWebsocketReconnect))
	wsClient.reconnect = newReconnector(ac, "sess-1")
	wsClient.reconnect.baseDelay = time.Millisecond
	wsClient.reconnect.notice = io.Discard

	require.NoError(t, wsClient.dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/"))

	served := make(chan struct{})
	go func() {
		defer close(served)
		wsClient.serveConnections(make(chan string))
	}()
	t.Cleanup(func() {
		wsClient.finish(nil)
		<-served
		wsClient.closeConn()
	})

	// The size lands only on a connection that is already up, so waiting for it
	// waits for the whole recovery.
	select {
	case size := <-resizes:
		assert.Equal(t, SessionSizeRequest{Rows: 40, Cols: 120}, size)
	case <-time.After(teardownWait):
		require.Fail(t, "the reconnected session never re-sent the terminal size",
			"upgrades=%d", upgrades.Load())
	}

	select {
	case req := <-channels:
		assert.Equal(t, ConnectRequest{Session: "sess-1", IsMaster: true}, req)
	default:
		require.Fail(t, "the reconnect dialed without provisioning a new channel")
	}

	assert.Equal(t, int32(2), upgrades.Load(), "the client must dial again rather than reuse the closed connection")
	for dial := 1; dial <= 2; dial++ {
		select {
		case header := <-dialHeaders:
			assert.Equal(t, client.CapabilityWebsocketReconnect,
				header.Get(client.ClientCapabilitiesHeader), "dial %d", dial)
			assert.Equal(t, "alpacon-cli/test", header.Get("User-Agent"), "dial %d", dial)
		default:
			require.Fail(t, "the server saw fewer dials than the client made", "dial %d", dial)
		}
	}

	select {
	case <-wsClient.done:
		require.Fail(t, "a service restart must not end the session", "err=%v", wsClient.err)
	default:
	}
}

func TestRedial_StopsWhenTheServerRefusesANewChannel(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail": "session is closed"}`))
	}))
	t.Cleanup(ts.Close)

	wsClient := newTestReconnectClient(ts.URL)

	assert.False(t, wsClient.redial())
	assertReported(t, wsClient)
	require.ErrorIs(t, wsClient.err, ErrSessionGone)
	// The session was closed while the link was down; asking again only asks again.
	assert.Equal(t, int32(1), attempts.Load())
}

func TestRedial_SpendsTheBudgetOnAServerThatKeepsFailing(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "service unavailable"}`))
	}))
	t.Cleanup(ts.Close)

	wsClient := newTestReconnectClient(ts.URL)

	assert.False(t, wsClient.redial())
	assertReported(t, wsClient)
	require.ErrorIs(t, wsClient.err, ErrReconnectFailed)
	assert.Equal(t, int32(maxReconnectAttempts), attempts.Load(), "a 5xx is worth the whole budget")
}

func TestRedial_StopsWhenTheSessionEndsMidWait(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "service unavailable"}`))
	}))
	t.Cleanup(ts.Close)

	wsClient := newTestReconnectClient(ts.URL)
	// Ctrl+C during the backoff: the wait has to end with the session, not carry
	// on dialing for a shell nobody is watching.
	wsClient.reconnect.baseDelay = teardownWait
	wsClient.finish(nil)

	awaitReturn(t, "redial parked on the backoff after the session ended", func() {
		assert.False(t, wsClient.redial())
	})
	assert.Zero(t, attempts.Load())
}

// newTestReconnectClient is a client whose reconnect talks to ts and waits in
// milliseconds rather than seconds.
func newTestReconnectClient(baseURL string) *WebsocketClient {
	ac := &client.AlpaconClient{HTTPClient: &http.Client{}, BaseURL: baseURL}

	wsClient := newWebsocketClient(nil)
	wsClient.reconnect = newReconnector(ac, "sess-1")
	wsClient.reconnect.baseDelay = time.Millisecond
	wsClient.reconnect.notice = io.Discard

	return wsClient
}

// pinTerminalSize replaces the terminal size lookup, which a test process has no
// terminal to answer.
func pinTerminalSize(t *testing.T, rows, cols int) {
	t.Helper()

	original := terminalSize
	terminalSize = func() (int, int, error) { return rows, cols, nil }
	t.Cleanup(func() { terminalSize = original })
}

// What reaches the server on the real entry points' first dial. Raw mode fails on
// a pipe, so each call returns right after the handshake the assertions read.
func TestOpenTerminal_AdvertisesTheCapabilityOnlyWhereItReconnects(t *testing.T) {
	tests := []struct {
		name string
		open func(*client.AlpaconClient, SessionResponse) error
		want string
	}{
		{
			name: "an interactive session reconnects",
			open: OpenNewTerminal,
			want: client.CapabilityWebsocketReconnect,
		},
		{
			// The joiner may not open a channel on someone else's session, so
			// claiming the capability would promise a recovery it cannot make.
			name: "a joined session does not",
			open: OpenSharedTerminal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialHeaders := make(chan http.Header, receivedBufferSize)
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case dialHeaders <- r.Header.Clone():
				default:
				}
				ws, err := upgrader.Upgrade(w, r, nil)
				if !assert.NoError(t, err) {
					return
				}
				_ = ws.Close() // the handler must not outlive the assertions below
			}))
			t.Cleanup(ts.Close)

			pipeStdin(t) // a pipe is not a tty, so raw mode fails after the dial succeeds

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, UserAgent: "alpacon-cli/test"}
			err := tt.open(ac, SessionResponse{
				ID:           "sess-1",
				WebsocketURL: "ws" + strings.TrimPrefix(ts.URL, "http"),
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "stdin is not a terminal")

			select {
			case header := <-dialHeaders:
				assert.Equal(t, tt.want, header.Get(client.ClientCapabilitiesHeader))
				assert.Equal(t, "alpacon-cli/test", header.Get("User-Agent"))
			default:
				require.Fail(t, "the server saw no dial")
			}
		})
	}
}
