package websh

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
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
		assert.Contains(t, r.URL.Path, "sess-abc/invite")

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
		assert.Contains(t, r.URL.Path, "chan-id-123/join")

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
				pipeWrite := pipeStdin(t)
				require.NoError(t, pipeWrite.Close()) // stdin yields EOF right away

				return func() { wsClient.readUserInput(make(chan string, 1)) }
			},
		},
		{
			name: "readUserInput sending to inputChan",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
				pipeWrite := pipeStdin(t)
				_, err := pipeWrite.WriteString("x")
				require.NoError(t, err)

				inputChan := make(chan string, 1)
				inputChan <- "buffered" // writeToServer has returned, so nothing drains this

				return func() { wsClient.readUserInput(inputChan) }
			},
		},
		{
			name: "writeToServer waiting to flush",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
				// Empty, so only the done branch can end the loop: nothing is ever flushed.
				return func() { wsClient.writeToServer(make(chan string)) }
			},
		},
		{
			name: "watchInterrupt waiting for a signal",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
				// No signal ever arrives, so only the done branch can release the watcher.
				return func() { wsClient.watchInterrupt(make(chan os.Signal)) }
			},
		},
		{
			name: "readCtrlC waiting for the next byte",
			start: func(t *testing.T, wsClient *WebsocketClient) func() {
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
	conn, _ := dialTestServer(t)
	require.NoError(t, conn.Close()) // every later WriteMessage fails

	wsClient := newWebsocketClient(nil)
	wsClient.conn = conn

	inputChan := make(chan string, 1)
	inputChan <- "x" // buffered input forces the failing write

	awaitReturn(t, "writeToServer parked on the failing write", func() {
		wsClient.writeToServer(inputChan)
	})

	assertReported(t, wsClient)
	assert.Error(t, wsClient.err)
}

func TestReadFromServer_ReportsReadFailure(t *testing.T) {
	conn, _ := dialTestServer(t)
	require.NoError(t, conn.Close()) // every later ReadMessage fails

	wsClient := newWebsocketClient(nil)
	wsClient.conn = conn

	awaitReturn(t, "readFromServer parked on the failing read", func() {
		wsClient.readFromServer()
	})

	assertReported(t, wsClient)
	assert.Error(t, wsClient.err)
}

func TestReadFromServer_PrintsEveryMessage(t *testing.T) {
	conn, _ := dialTestServer(t, "hi ", "there")

	wsClient := newWebsocketClient(nil)
	wsClient.conn = conn

	stdout := testutil.CaptureStdout(t, func() {
		awaitReturn(t, "readFromServer parked after the server went away", func() {
			wsClient.readFromServer()
		})
	})

	assert.Equal(t, "hi there", stdout) // the loop must survive a successful read
	assertReported(t, wsClient)
	assert.Error(t, wsClient.err)
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
	conn, received := dialTestServer(t)

	wsClient := newWebsocketClient(nil)
	wsClient.conn = conn
	t.Cleanup(func() { wsClient.finish(nil) })

	inputChan := make(chan string, 2)
	inputChan <- "l"
	inputChan <- "s"
	go wsClient.writeToServer(inputChan)

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
	wsClient := newWebsocketClient(nil)

	first := errors.New("remote closed the session")
	wsClient.finish(first)
	wsClient.finish(errors.New("write failed on the closed connection"))

	assertReported(t, wsClient)
	assert.Equal(t, first, wsClient.err)
}

func TestFinish_NormalizesARemoteCloseToASuccess(t *testing.T) {
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
