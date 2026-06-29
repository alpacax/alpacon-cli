package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEventList_NoExtraPagination(t *testing.T) {
	var eventRequestCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		count := eventRequestCount.Add(1)
		if count > 1 {
			t.Errorf("extra request detected: request #%d to %s (should be single request)", count, r.URL.String())
			return
		}

		pageSize := r.URL.Query().Get("page_size")
		if pageSize != "25" {
			t.Errorf("expected page_size=25, got %s", pageSize)
		}

		var results []EventDetails
		for i := range 25 {
			results = append(results, EventDetails{
				ID:          fmt.Sprintf("evt-%d", i),
				Server:      types.ServerSummary{Name: "test-server"},
				Shell:       "bash",
				Line:        fmt.Sprintf("cmd-%d", i),
				RequestedBy: types.UserSummary{Name: "admin"},
			})
		}

		resp := api.ListResponse[EventDetails]{
			Count:   200, // more items exist on server
			Results: results,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	events, err := GetEventList(ac, 25, "", "")
	if err != nil {
		t.Fatalf("GetEventList error: %v", err)
	}

	totalRequests := int(eventRequestCount.Load())
	if totalRequests != 1 {
		t.Errorf("expected 1 request, got %d", totalRequests)
	}
	if len(events) != 25 {
		t.Errorf("expected 25 events, got %d", len(events))
	}
}

func TestPollCommandExecution(t *testing.T) {
	tests := []struct {
		name           string
		statusSequence []string
		wantStatus     string
		wantResult     string
		wantRequests   int
	}{
		{
			name:           "running then completed",
			statusSequence: []string{"running", "running", "completed"},
			wantStatus:     "completed",
			wantResult:     "done",
			wantRequests:   3,
		},
		{
			name:           "acked then completed (backwards compat)",
			statusSequence: []string{"acked", "completed"},
			wantStatus:     "completed",
			wantResult:     "done",
			wantRequests:   2,
		},
		{
			name:           "immediate terminal status",
			statusSequence: []string{"error"},
			wantStatus:     "error",
			wantResult:     "done",
			wantRequests:   1,
		},
		{
			name:           "queued then delivered then running then success",
			statusSequence: []string{"queued", "delivered", "running", "success"},
			wantStatus:     "success",
			wantResult:     "done",
			wantRequests:   4,
		},
		{
			name:           "scheduled then queued then success",
			statusSequence: []string{"scheduled", "queued", "success"},
			wantStatus:     "success",
			wantResult:     "done",
			wantRequests:   3,
		},
		{
			name:           "verifying then running then success",
			statusSequence: []string{"verifying", "running", "success"},
			wantStatus:     "success",
			wantResult:     "done",
			wantRequests:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqCount atomic.Int32

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(reqCount.Add(1)) - 1
				if idx >= len(tt.statusSequence) {
					idx = len(tt.statusSequence) - 1
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(EventDetails{
					ID:     "cmd-1",
					Status: tt.statusSequence[idx],
					Result: "done",
				})
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{
				HTTPClient: ts.Client(),
				BaseURL:    ts.URL,
			}

			result, err := pollCommandExecution(ac, "cmd-1", 30*time.Second, 5*time.Millisecond, false)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, tt.wantResult, result.Result)
			assert.Equal(t, tt.wantRequests, int(reqCount.Load()))
		})
	}
}

// runCommandBodyCapture holds the captured POST body fields for the
// /api/events/commands/ request. Access is guarded by mu because the
// test server handler runs on a separate goroutine from the test body.
type runCommandBodyCapture struct {
	mu                sync.Mutex
	hadWorkSessionKey bool
	workSession       string
	postSeen          bool
}

func (c *runCommandBodyCapture) record(payload map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := payload["work_session"]
	c.hadWorkSessionKey = ok
	if ok {
		c.workSession, _ = v.(string)
	}
	c.postSeen = true
}

func (c *runCommandBodyCapture) snapshot() (hadKey bool, ws string, seen bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hadWorkSessionKey, c.workSession, c.postSeen
}

// newRunCommandBodyCaptureServer returns a test server that responds to
// GET /api/servers/servers/?name=... with a 1-item server list and captures the
// POST body for /api/events/commands/, returning a minimal CommandResponse list.
func newRunCommandBodyCaptureServer(t *testing.T, capture *runCommandBodyCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/servers/servers/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
				Count: 1,
				Results: []map[string]any{
					{"id": "srv-1", "name": "server-x"},
				},
			})
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/events/commands/") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			capture.record(payload)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "cmd-1"}})
			return
		}
		http.NotFound(w, r)
	}))
}

func TestSubmitCommand_BodyIncludesWorkSession_WhenSet(t *testing.T) {
	var capture runCommandBodyCapture
	ts := newRunCommandBodyCaptureServer(t, &capture)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := SubmitCommand(ac, "server-x", "ls", "", "", nil, "ses-abc")
	require.NoError(t, err)

	hadKey, ws, _ := capture.snapshot()
	require.True(t, hadKey, "body must contain work_session field when ID is set")
	assert.Equal(t, "ses-abc", ws)
}

func TestSubmitCommand_BodyOmitsWorkSession_WhenEmpty(t *testing.T) {
	var capture runCommandBodyCapture
	ts := newRunCommandBodyCaptureServer(t, &capture)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := SubmitCommand(ac, "server-x", "ls", "", "", nil, "")
	require.NoError(t, err)

	hadKey, _, _ := capture.snapshot()
	assert.False(t, hadKey, "body must omit work_session field when ID is empty")
}

func TestErrorFromDetails_PropagatesExitCode(t *testing.T) {
	tests := []struct {
		name           string
		respSuccess    *bool
		respExitCode   *int
		respErrorPhase *string
		respResult     string
		wantExitCode   int
		wantErrorPhase string
		wantOutput     string
	}{
		{
			name:         "exit_1",
			respSuccess:  boolPtr(false),
			respExitCode: intPtr(1),
			respResult:   "boom",
			wantExitCode: 1,
			wantOutput:   "boom",
		},
		{
			name:         "exit_23_rsync_partial",
			respSuccess:  boolPtr(false),
			respExitCode: intPtr(23),
			respResult:   "rsync: partial transfer",
			wantExitCode: 23,
			wantOutput:   "rsync: partial transfer",
		},
		{
			name:           "exit_124_with_phase",
			respSuccess:    boolPtr(false),
			respExitCode:   intPtr(124),
			respErrorPhase: strPtr("remote_command_exceeded_timeout"),
			respResult:     "timed out",
			wantExitCode:   124,
			wantErrorPhase: "remote_command_exceeded_timeout",
			wantOutput:     "timed out",
		},
		{
			name:         "legacy_null_exit_code_falls_back_to_1",
			respSuccess:  boolPtr(false),
			respExitCode: nil,
			respResult:   "old alpamon",
			wantExitCode: 1,
			wantOutput:   "old alpamon",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errorFromDetails(EventDetails{
				Status:     "completed",
				Success:    tt.respSuccess,
				ExitCode:   tt.respExitCode,
				ErrorPhase: tt.respErrorPhase,
				Result:     tt.respResult,
			})
			require.Error(t, err)

			var remoteErr *RemoteCommandError
			require.True(t, errors.As(err, &remoteErr), "err must be *RemoteCommandError")
			assert.Equal(t, tt.wantOutput, remoteErr.Output)
			assert.Equal(t, tt.wantExitCode, remoteErr.ExitCode)
			assert.Equal(t, tt.wantErrorPhase, remoteErr.ErrorPhase)
		})
	}
}

func TestErrorFromDetails_AwaitingApprovalReturnsPendingApprovalError(t *testing.T) {
	err := errorFromDetails(EventDetails{ID: "cmd-9", Status: "awaiting_approval"})
	var pending *PendingApprovalError
	require.True(t, errors.As(err, &pending), "err must be *PendingApprovalError")
	assert.Equal(t, "cmd-9", pending.CommandID)
}

func TestErrorFromDetails_RejectedReturnsError(t *testing.T) {
	err := errorFromDetails(EventDetails{ID: "cmd-9", Status: "rejected"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected by a reviewer")
}

func TestPollCommandExecution_WaitApprovalResumesAfterApproval(t *testing.T) {
	// awaiting_approval, then the transient "error" the server emits in the
	// approve→deliver window, then completed: waitApproval must poll through both.
	seq := []string{"awaiting_approval", "awaiting_approval", "error", "completed"}
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		i := int(calls.Add(1)) - 1
		if i >= len(seq) {
			i = len(seq) - 1
		}
		_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: seq[i], Success: boolPtr(true)})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := pollCommandExecution(ac, "cmd-1", time.Second, 5*time.Millisecond, true)
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
}

func TestStreamApprovedCommand_StreamsAfterApproval(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:        "cmd-uuid",
		serverID:     "srv-uuid",
		wsChunks:     []ChunkEvent{{Seq: 0, Content: "approved\n"}},
		heldPolls:    2,
		runningPolls: 1,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true)},
	})

	err := StreamApprovedCommand(ac, "cmd-uuid", stdoutBuf, 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "approved\n", stdoutBuf.String())
}

func boolPtr(b bool) *bool    { return &b }
func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }

func TestPollCommandExecution_ClientTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always fail GETs so SendGetRequest returns an error and the poll
		// loop never resets its timer.
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := pollCommandExecution(ac, "cmd-1", 50*time.Millisecond, 10*time.Millisecond, false)
	require.Error(t, err)

	var clientTimeout *ClientTimeoutError
	require.True(t, errors.As(err, &clientTimeout),
		"timer expiry must surface a *ClientTimeoutError, got %T: %v", err, err)
}

func TestPollCommandExecution_TerminalStatusReturnsBeforeTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "completed"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := pollCommandExecution(ac, "cmd-1", 50*time.Millisecond, 5*time.Millisecond, false)
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
}

func TestSubmitCommand_ReturnsJobID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/servers/servers/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
				Count:   1,
				Results: []map[string]any{{"id": "srv-1", "name": "server-x"}},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/events/commands/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "job-abc-123"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := SubmitCommand(ac, "server-x", "apt upgrade", "", "", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "job-abc-123", resp.ID)
}

func TestGetCommandByID_ReturnsEventDetails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/events/commands/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(EventDetails{
				ID:     "job-abc-123",
				Status: "completed",
				Result: "Packages updated.",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	details, err := GetCommandByID(ac, "job-abc-123")
	require.NoError(t, err)
	assert.Equal(t, "job-abc-123", details.ID)
	assert.Equal(t, "completed", details.Status)
	assert.Equal(t, "Packages updated.", details.Result)
}

func TestGetCommandByID_PropagatesError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := GetCommandByID(ac, "job-abc-123")
	require.Error(t, err)
}

func TestExecTimeout_Default(t *testing.T) {
	t.Setenv("ALPACON_EXEC_TIMEOUT", "")
	assert.Equal(t, 30*time.Minute, execTimeout())
}

func TestExecTimeout_EnvVar(t *testing.T) {
	t.Setenv("ALPACON_EXEC_TIMEOUT", "1h")
	assert.Equal(t, time.Hour, execTimeout())
}

func TestExecTimeout_InvalidEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("ALPACON_EXEC_TIMEOUT", "not-a-duration")
	assert.Equal(t, 30*time.Minute, execTimeout())
}

func TestSubmitCommand_401WithDetailSurfacesServerReason(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/servers/servers/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
				Count:   1,
				Results: []map[string]any{{"id": "srv-1", "name": "server-x"}},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/events/commands/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail": "user 'root' on server-x: denied by policy (no matching sudo/role rule)"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := SubmitCommand(ac, "server-x", "id", "root", "", nil, "ses-abc")
	require.Error(t, err)

	assert.Contains(t, err.Error(), "denied by policy")
	assert.NotContains(t, err.Error(), "authentication failed")
	assert.Contains(t, err.Error(), "alpacon login")
}

func TestRunCommandStreaming_NormalFlow(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:        "cmd-uuid",
		serverID:     "srv-uuid",
		wsChunks:     []ChunkEvent{{Seq: 0, Content: "hello\n"}, {Seq: 1, Content: "world\n"}},
		runningPolls: 2,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true)},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "hello\nworld\n", stdoutBuf.String())
}

func TestRunCommandStreaming_GapFilledByREST(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:    "cmd-uuid",
		serverID: "srv-uuid",
		// WS delivers seq 0 then 3; the missing 1,2 come from the gap-fill fetch.
		wsChunks: []ChunkEvent{{Seq: 0, Content: "s0\n"}, {Seq: 3, Content: "s3\n"}},
		chunksFor: func(fromSeq int) []Chunk {
			if fromSeq == 1 {
				return []Chunk{{Seq: 1, Content: "s1\n"}, {Seq: 2, Content: "s2\n"}}
			}
			return nil
		},
		runningPolls: 2,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true)},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "s0\ns1\ns2\ns3\n", stdoutBuf.String())
}

// TestRunCommandStreaming_WarmFireGapDoesNotSkipLaterChunk guards against
// advancing lastSeq past a gap during the warm-fire drain. If the persisted
// chunks contain a hole (seq 0,1,3 with 2 missing), lastSeq must stop at the
// last contiguous seq (1) so a later seq 2 arriving over the WS is still
// written rather than skipped as a duplicate. seq 3 is then filled by the
// terminal drain in order.
func TestRunCommandStreaming_WarmFireGapDoesNotSkipLaterChunk(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:    "cmd-uuid",
		serverID: "srv-uuid",
		// seq 2 is absent from the persisted set; it arrives only over the WS.
		wsChunks: []ChunkEvent{{Seq: 2, Content: "s2\n"}},
		chunksFor: func(fromSeq int) []Chunk {
			persisted := []Chunk{{Seq: 0, Content: "s0\n"}, {Seq: 1, Content: "s1\n"}, {Seq: 3, Content: "s3\n"}}
			var out []Chunk
			for _, c := range persisted {
				if c.Seq >= fromSeq {
					out = append(out, c)
				}
			}
			return out
		},
		runningPolls: 1,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true)},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "s0\ns1\ns2\ns3\n", stdoutBuf.String())
}

func TestRunCommandStreaming_DuplicateSeqIgnored(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:    "cmd-uuid",
		serverID: "srv-uuid",
		// The second seq 1 must be dropped.
		wsChunks: []ChunkEvent{
			{Seq: 0, Content: "s0\n"},
			{Seq: 1, Content: "s1\n"},
			{Seq: 1, Content: "s1\n"},
			{Seq: 2, Content: "s2\n"},
		},
		runningPolls: 2,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true)},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "s0\ns1\ns2\n", stdoutBuf.String())
}

// TestRunCommandStreaming_FailedStatusPropagatesExitCode guards that terminal
// status "failed" yields a *RemoteCommandError carrying the exit code.
func TestRunCommandStreaming_FailedStatusPropagatesExitCode(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:        "cmd-uuid",
		serverID:     "srv-uuid",
		wsChunks:     []ChunkEvent{{Seq: 0, Content: "before-exit\n"}},
		runningPolls: 1,
		terminal:     EventDetails{Status: "failed", Success: boolPtr(false), ExitCode: intPtr(23)},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "exit 23", "", "", nil, "", stdoutBuf)
	require.Error(t, err)
	var remoteErr *RemoteCommandError
	require.True(t, errors.As(err, &remoteErr), "failed status must yield *RemoteCommandError")
	assert.Equal(t, 23, remoteErr.ExitCode)
	assert.Equal(t, "before-exit\n", stdoutBuf.String())
}

// TestRunCommandStreaming_GapFillRaceDoesNotDropChunk guards that when a gap-fill
// fetch returns a hole (seq 1,3; 2 not yet persisted), applyChunk stops at the
// hole so the later WS seq 2 isn't dropped as a duplicate.
func TestRunCommandStreaming_GapFillRaceDoesNotDropChunk(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:    "cmd-uuid",
		serverID: "srv-uuid",
		// WS order 0,3,2: seq 3 opens a gap; seq 2 arrives only afterwards.
		wsChunks: []ChunkEvent{{Seq: 0, Content: "s0\n"}, {Seq: 3, Content: "s3\n"}, {Seq: 2, Content: "s2\n"}},
		chunksFor: func(fromSeq int) []Chunk {
			persisted := []Chunk{{Seq: 1, Content: "s1\n"}, {Seq: 2, Content: "s2\n"}, {Seq: 3, Content: "s3\n"}}
			if fromSeq == 1 {
				// Race: seq 2 is not yet persisted when the gap-fill fetch runs.
				return []Chunk{{Seq: 1, Content: "s1\n"}, {Seq: 3, Content: "s3\n"}}
			}
			var out []Chunk
			for _, c := range persisted {
				if c.Seq >= fromSeq {
					out = append(out, c)
				}
			}
			return out
		},
		runningPolls: 3,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true)},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "seq", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "s0\ns1\ns2\ns3\n", stdoutBuf.String())
}

func TestRunCommandStreaming_FallbackOnSubscribeFailureReusesCommand(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	cmdID := "cmd-uuid"
	serverID := "srv-uuid"

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	// WS server just upgrades and waits; subscribe will fail so chunks shouldn't matter
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer wsServer.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	var submitCount int
	var pollCount int
	var mu sync.Mutex

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"` + serverID + `","name":"srv"}]}`))
		case "/api/events/sessions/":
			_, _ = w.Write([]byte(`{"id":"s","websocket_url":"` + wsURL + `","channel_id":"ch"}`))
		case "/api/events/commands/":
			mu.Lock()
			submitCount++
			mu.Unlock()
			_, _ = w.Write([]byte(`[{"id":"` + cmdID + `"}]`))
		case "/api/events/subscriptions/":
			// Force subscribe failure -> fallback path with existing cmdID
			w.WriteHeader(http.StatusInternalServerError)
		case "/api/events/commands/" + cmdID + "/chunks/":
			// No chunks persisted: fallback must use the buffered Result.
			_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{})
		case "/api/events/commands/" + cmdID + "/":
			mu.Lock()
			pollCount++
			n := pollCount
			mu.Unlock()
			if n < 2 {
				_, _ = w.Write([]byte(`{"id":"` + cmdID + `","status":"running"}`))
			} else {
				success := true
				resp := EventDetails{ID: cmdID, Status: "completed", Success: &success, Result: "reused-output\n"}
				_ = json.NewEncoder(w).Encode(resp)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiServer.Close()

	ac := &client.AlpaconClient{HTTPClient: apiServer.Client(), BaseURL: apiServer.URL}

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "reused-output\n", stdoutBuf.String())

	// Key assertion: SubmitCommand was called exactly once
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, submitCount, "fallback must not re-submit the command")
}

// TestRunCommandStreaming_FallbackDrainsChunks guards that the polling fallback
// reconstructs output from chunks when the server leaves Result empty (the
// chunk-streaming contract), instead of silently dropping it.
func TestRunCommandStreaming_FallbackDrainsChunks(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	cmdID := "cmd-uuid"
	serverID := "srv-uuid"

	var pollCount int
	var mu sync.Mutex
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"` + serverID + `","name":"srv"}]}`))
		case "/api/events/sessions/":
			w.WriteHeader(http.StatusInternalServerError) // force fallback
		case "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"` + cmdID + `"}]`))
		case "/api/events/commands/" + cmdID + "/chunks/":
			resp := api.ListResponse[Chunk]{Count: 2, Results: []Chunk{{Seq: 0, Content: "chunk-a\n"}, {Seq: 1, Content: "chunk-b\n"}}}
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/events/commands/" + cmdID + "/":
			mu.Lock()
			pollCount++
			n := pollCount
			mu.Unlock()
			if n < 2 {
				_, _ = w.Write([]byte(`{"id":"` + cmdID + `","status":"running"}`))
			} else {
				// Result empty: output lives only in chunks (server contract).
				_ = json.NewEncoder(w).Encode(EventDetails{ID: cmdID, Status: "completed", Success: boolPtr(true), Result: ""})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiServer.Close()

	ac := &client.AlpaconClient{HTTPClient: apiServer.Client(), BaseURL: apiServer.URL}

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "chunk-a\nchunk-b\n", stdoutBuf.String())
}

func TestRunCommandStreaming_FallbackOnSessionFailure(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	cmdID := "cmd-uuid"
	serverID := "srv-uuid"

	var pollCount int
	var mu sync.Mutex
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"` + serverID + `","name":"srv"}]}`))
		case "/api/events/sessions/":
			// Force fallback
			w.WriteHeader(http.StatusInternalServerError)
		case "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"` + cmdID + `"}]`))
		case "/api/events/commands/" + cmdID + "/chunks/":
			// No chunks persisted: fallback must use the buffered Result.
			_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{})
		case "/api/events/commands/" + cmdID + "/":
			mu.Lock()
			pollCount++
			n := pollCount
			mu.Unlock()
			if n < 2 {
				_, _ = w.Write([]byte(`{"id":"` + cmdID + `","status":"running"}`))
			} else {
				success := true
				resp := EventDetails{ID: cmdID, Status: "completed", Success: &success, Result: "fallback-output\n"}
				_ = json.NewEncoder(w).Encode(resp)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiServer.Close()

	ac := &client.AlpaconClient{HTTPClient: apiServer.Client(), BaseURL: apiServer.URL}

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "fallback-output\n", stdoutBuf.String())
}

// TestRunCommandStreaming_FallbackQuietWhenChunksUnavailable: when the chunks
// endpoint errors, the polling fallback emits the buffered Result, not an error.
func TestRunCommandStreaming_FallbackQuietWhenChunksUnavailable(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	cmdID := "cmd-uuid"
	serverID := "srv-uuid"

	var pollCount int
	var mu sync.Mutex
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"` + serverID + `","name":"srv"}]}`))
		case "/api/events/sessions/":
			w.WriteHeader(http.StatusInternalServerError) // force fallback
		case "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"` + cmdID + `"}]`))
		case "/api/events/commands/" + cmdID + "/chunks/":
			w.WriteHeader(http.StatusNotFound) // server without chunk support
		case "/api/events/commands/" + cmdID + "/":
			mu.Lock()
			pollCount++
			n := pollCount
			mu.Unlock()
			if n < 2 {
				_, _ = w.Write([]byte(`{"id":"` + cmdID + `","status":"running"}`))
			} else {
				success := true
				resp := EventDetails{ID: cmdID, Status: "completed", Success: &success, Result: "buffered-output\n"}
				_ = json.NewEncoder(w).Encode(resp)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiServer.Close()

	ac := &client.AlpaconClient{HTTPClient: apiServer.Client(), BaseURL: apiServer.URL}

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "buffered-output\n", stdoutBuf.String())
}

// streamingServerConfig configures the fake event servers for streaming tests.
type streamingServerConfig struct {
	cmdID        string
	serverID     string
	wsChunks     []ChunkEvent      // emitted over the WS once subscribed
	chunksFor    func(int) []Chunk // REST chunk endpoint, keyed by seq__gte (warm-fire / gap-fill / drain)
	heldPolls    int               // number of "awaiting_approval" detail polls before running
	runningPolls int               // number of "running" detail polls before the terminal one
	terminal     EventDetails      // returned by the detail poll once held+running elapse
}

// newStreamingServers starts a WS + API server pair and returns a client for
// them. The WS emits cfg.wsChunks after the subscription POST arrives.
func newStreamingServers(t *testing.T, cfg streamingServerConfig) *client.AlpaconClient {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	subscribed := make(chan struct{})
	var subOnce sync.Once

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		select {
		case <-subscribed:
		case <-time.After(10 * time.Second):
			t.Error("timeout waiting for subscription signal")
			return
		}
		for _, c := range cfg.wsChunks {
			env := map[string]any{
				"event_type": "command_output",
				"payload":    map[string]any{"command_id": cfg.cmdID, "seq": c.Seq, "content": c.Content},
			}
			b, _ := json.Marshal(env)
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(wsServer.Close)
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	var pollCount int
	var mu sync.Mutex
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/servers/servers/" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"` + cfg.serverID + `","name":"srv"}]}`))
		case r.URL.Path == "/api/events/sessions/" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"s","websocket_url":"` + wsURL + `","channel_id":"ch"}`))
		case r.URL.Path == "/api/events/commands/" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`[{"id":"` + cfg.cmdID + `"}]`))
		case r.URL.Path == "/api/events/subscriptions/" && r.Method == http.MethodPost:
			subOnce.Do(func() { close(subscribed) })
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/api/events/commands/"+cfg.cmdID+"/chunks/" && r.Method == http.MethodGet:
			fromSeq, _ := strconv.Atoi(r.URL.Query().Get("seq__gte"))
			var results []Chunk
			if cfg.chunksFor != nil {
				results = cfg.chunksFor(fromSeq)
			}
			_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{Count: len(results), Results: results})
		case r.URL.Path == "/api/events/commands/"+cfg.cmdID+"/" && r.Method == http.MethodGet:
			mu.Lock()
			pollCount++
			n := pollCount
			mu.Unlock()
			if n <= cfg.heldPolls {
				_, _ = w.Write([]byte(`{"id":"` + cfg.cmdID + `","status":"awaiting_approval"}`))
				return
			}
			if n <= cfg.heldPolls+cfg.runningPolls {
				_, _ = w.Write([]byte(`{"id":"` + cfg.cmdID + `","status":"running"}`))
				return
			}
			term := cfg.terminal
			term.ID = cfg.cmdID
			_ = json.NewEncoder(w).Encode(term)
		default:
			t.Logf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(apiServer.Close)

	return &client.AlpaconClient{HTTPClient: apiServer.Client(), BaseURL: apiServer.URL}
}

// TestRunCommandStreaming_NoDuplicateOutputOnFailure guards the duplicate-output
// fix: a failed command's buffered Result must not be re-written to stdout after
// the chunks were already streamed. The Result is still carried on the error so
// cmd/exec can inspect it (e.g. for the sudo-denial hint) without reprinting.
func TestRunCommandStreaming_NoDuplicateOutputOnFailure(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:        "cmd-uuid",
		serverID:     "srv-uuid",
		wsChunks:     []ChunkEvent{{Seq: 0, Content: "hello\n"}, {Seq: 1, Content: "world\n"}},
		runningPolls: 2,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(false), Result: "hello\nworld\n"},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)

	// Streamed once: the buffered Result is not appended to the writer.
	assert.Equal(t, "hello\nworld\n", stdoutBuf.String())
	// Retained on the error for inspection (cmd/exec must not reprint it).
	var remoteErr *RemoteCommandError
	require.ErrorAs(t, err, &remoteErr)
	assert.Equal(t, "hello\nworld\n", remoteErr.Output)
}

// TestRunCommandStreaming_TerminalStatusErrors covers errorFromDetails' non-nil
// branches reached through the streaming select loop.
func TestRunCommandStreaming_TerminalStatusErrors(t *testing.T) {
	tests := []struct {
		name     string
		terminal EventDetails
		check    func(t *testing.T, err error)
	}{
		{
			name:     "success false returns RemoteCommandError with exit code",
			terminal: EventDetails{Status: "completed", Success: boolPtr(false), ExitCode: intPtr(7)},
			check: func(t *testing.T, err error) {
				var re *RemoteCommandError
				require.ErrorAs(t, err, &re)
				assert.Equal(t, 7, re.ExitCode)
			},
		},
		{
			name:     "stuck status without phase keeps legacy message",
			terminal: EventDetails{Status: "stuck"},
			check: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "command failed with status: stuck")
			},
		},
		{
			name:     "error status without phase keeps legacy message",
			terminal: EventDetails{Status: "error"},
			check: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "command failed with status: error")
			},
		},
		{
			name:     "cancelled status without phase keeps legacy message",
			terminal: EventDetails{Status: "cancelled"},
			check: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "command failed with status: cancelled")
			},
		},
		{
			name:     "stuck status with phase carries phase identifier",
			terminal: EventDetails{Status: "stuck", ErrorPhase: strPtr("agent_timeout")},
			check: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "[agent_timeout]")
				assert.Contains(t, err.Error(), "status=stuck")
			},
		},
		{
			name:     "unrecognized status returns unexpected-status error",
			terminal: EventDetails{Status: "denied"},
			check: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unexpected command status")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdoutBuf := &bytes.Buffer{}
			ac := newStreamingServers(t, streamingServerConfig{
				cmdID:        "cmd-uuid",
				serverID:     "srv-uuid",
				runningPolls: 1,
				terminal:     tt.terminal,
			})
			err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
			tt.check(t, err)
		})
	}
}

// TestRunCommandStreaming_DrainsTrailingChunksOnTerminal covers the drain path:
// trailing chunks never seen over the WS are recovered by the final REST drain.
func TestRunCommandStreaming_DrainsTrailingChunksOnTerminal(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:    "cmd-uuid",
		serverID: "srv-uuid",
		wsChunks: []ChunkEvent{{Seq: 0, Content: "s0\n"}},
		chunksFor: func(fromSeq int) []Chunk {
			// Warm-fire (seq>=0) is empty; the final drain (seq>=1) yields the tail.
			if fromSeq >= 1 {
				return []Chunk{{Seq: 1, Content: "s1\n"}, {Seq: 2, Content: "s2\n"}}
			}
			return nil
		},
		runningPolls: 2,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true)},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "s0\ns1\ns2\n", stdoutBuf.String())
}

// TestRunCommandStreaming_FallbackToResultWhenNothingStreamed covers the
// last-resort path: when no chunks arrive over the WS and none are persisted,
// the buffered Result must still be written so output is never silently dropped.
func TestRunCommandStreaming_FallbackToResultWhenNothingStreamed(t *testing.T) {
	stdoutBuf := &bytes.Buffer{}
	ac := newStreamingServers(t, streamingServerConfig{
		cmdID:        "cmd-uuid",
		serverID:     "srv-uuid",
		runningPolls: 1,
		terminal:     EventDetails{Status: "completed", Success: boolPtr(true), Result: "buffered-only\n"},
	})

	err := runCommandStreamingWithWriter(ac, "srv", "echo hi", "", "", nil, "", stdoutBuf)
	require.NoError(t, err)
	assert.Equal(t, "buffered-only\n", stdoutBuf.String())
}
