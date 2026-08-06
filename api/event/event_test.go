package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
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
		// Always fail GETs so SendGetRequest returns an error, and with a status
		// that is not 429 so the poll loop never extends its deadline.
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := pollCommandExecution(ac, "cmd-1", 50*time.Millisecond, 10*time.Millisecond, false)
	require.Error(t, err)

	var clientTimeout *ClientTimeoutError
	require.True(t, errors.As(err, &clientTimeout),
		"deadline expiry must surface a *ClientTimeoutError, got %T: %v", err, err)
}

func TestNextPollTick(t *testing.T) {
	base := time.Second
	tests := []struct {
		name    string
		elapsed time.Duration
		want    time.Duration
	}{
		{name: "base tick while the command is young", elapsed: 0, want: base},
		{name: "still base at the end of the fast window", elapsed: 9 * base, want: base},
		{name: "widens once the fast window closes", elapsed: 10 * base, want: 5 * base},
		{name: "holds through the medium window", elapsed: 59 * base, want: 5 * base},
		{name: "widest once the medium window closes", elapsed: 60 * base, want: 10 * base},
		{name: "stays widest", elapsed: 3000 * base, want: 10 * base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nextPollTick(base, tt.elapsed))
		})
	}
}

func TestNextPollBackoff(t *testing.T) {
	base := time.Second
	tests := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{name: "first failure keeps the base tick", attempt: 0, want: base},
		{name: "doubles per consecutive failure", attempt: 1, want: 2 * base},
		{name: "still doubling", attempt: 3, want: 8 * base},
		{name: "capped once doubling passes the cap", attempt: 6, want: 60 * base},
		{name: "stays capped", attempt: 20, want: 60 * base},
		{name: "server's Retry-After wins over the schedule", attempt: 0, retryAfter: 3 * base, want: 3 * base},
		{name: "Retry-After wins even when it is shorter", attempt: 5, retryAfter: 3 * base, want: 3 * base},
		{name: "Retry-After is capped too", attempt: 0, retryAfter: 3000 * base, want: 60 * base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nextPollBackoff(base, tt.attempt, tt.retryAfter))
		})
	}
}

// throttleServer replies 429 to the first throttled requests, then serves a
// terminal status. The returned counter holds the requests it has seen.
func throttleServer(t *testing.T, throttled int, retryAfter string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	reqCount := &atomic.Int32{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if int(reqCount.Add(1)) <= throttled {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{"detail": "Request was throttled."})
			return
		}
		_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "completed", Result: "done"})
	}))
	return ts, reqCount
}

// fakePollClock runs the poll loop on a clock only its own delays advance, and
// returns the delays it chose. Timing the loop instead would be flaky: a slow
// machine stretches every wait, which makes a loop without backoff look
// better-behaved than it is and can expire a deadline the loop meant to extend.
func fakePollClock(t *testing.T) func() []time.Duration {
	t.Helper()
	now := time.Now()
	var delays []time.Duration
	restoreNow, restoreSleep := pollNow, pollSleep
	pollNow = func() time.Time { return now }
	pollSleep = func(d time.Duration) {
		delays = append(delays, d)
		now = now.Add(d)
	}
	t.Cleanup(func() { pollNow, pollSleep = restoreNow, restoreSleep })
	return func() []time.Duration { return delays }
}

// A throttled poll must space its requests out instead of spending one per tick.
func TestPollCommandExecution_ThrottleBacksOff(t *testing.T) {
	tick := 10 * time.Millisecond
	tests := []struct {
		name       string
		retryAfter string
		want       []time.Duration
	}{
		{
			name: "no Retry-After: exponential backoff",
			want: []time.Duration{tick, tick, 2 * tick, 4 * tick, 8 * tick},
		},
		{
			name:       "Retry-After honored, capped",
			retryAfter: "1",
			want:       []time.Duration{tick, 60 * tick, 60 * tick, 60 * tick, 60 * tick},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, _ := throttleServer(t, 4, tt.retryAfter)
			defer ts.Close()

			delays := fakePollClock(t)

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			resp, err := pollCommandExecution(ac, "cmd-1", time.Minute, tick, false)
			require.NoError(t, err)
			assert.Equal(t, "completed", resp.Status)
			assert.Equal(t, tt.want, delays())
		})
	}
}

// A 429 must not starve the deadline: the command may already have finished,
// with only its result GET throttled.
func TestPollCommandExecution_ThrottleDoesNotStarveDeadline(t *testing.T) {
	ts, reqCount := throttleServer(t, 1, "1")
	defer ts.Close()

	// The Retry-After wait (1s, capped to 60 ticks = 300ms) outlasts the 100ms
	// deadline: reachable only if throttled time is not charged to it.
	delays := fakePollClock(t)

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := pollCommandExecution(ac, "cmd-1", 100*time.Millisecond, 5*time.Millisecond, false)
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
	assert.Equal(t, int32(2), reqCount.Load(),
		"the result must come from the retry after the throttled wait, not from retrying through it")
	assert.Equal(t, []time.Duration{5 * time.Millisecond, 300 * time.Millisecond}, delays())
}

// Extending the deadline by exactly the wait keeps it ahead of the clock forever,
// so the extension is capped: a token stuck over quota has to give up.
func TestPollCommandExecution_ThrottleExtensionIsBounded(t *testing.T) {
	ts, reqCount := throttleServer(t, math.MaxInt, "1")
	defer ts.Close()

	delays := fakePollClock(t)

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := pollCommandExecution(ac, "cmd-1", 100*time.Millisecond, 5*time.Millisecond, false)

	var clientTimeout *ClientTimeoutError
	require.True(t, errors.As(err, &clientTimeout),
		"a permanently throttled poll must surface a *ClientTimeoutError, got %T: %v", err, err)
	// One 300ms extension spends the 100ms budget; the second wait runs past the deadline.
	assert.Equal(t, int32(2), reqCount.Load())
	assert.Equal(t, []time.Duration{5 * time.Millisecond, 300 * time.Millisecond, 300 * time.Millisecond}, delays())
}

// Reported progress refreshes the deadline, and the throttle allowance goes back
// with it: a throttle late in a long command must not be refused an extension
// because an unrelated one early on spent the budget.
func TestPollCommandExecution_ProgressRestoresThrottleAllowance(t *testing.T) {
	reqCount := &atomic.Int32{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch reqCount.Add(1) {
		case 1, 3:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{"detail": "Request was throttled."})
		case 2:
			_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "running"})
		default:
			_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "completed"})
		}
	}))
	defer ts.Close()

	delays := fakePollClock(t)

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	resp, err := pollCommandExecution(ac, "cmd-1", 100*time.Millisecond, 5*time.Millisecond, false)
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
	assert.Equal(t, int32(4), reqCount.Load())
	assert.Equal(t, []time.Duration{
		5 * time.Millisecond, 300 * time.Millisecond, 50 * time.Millisecond, 300 * time.Millisecond,
	}, delays())
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

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what
// was written, so tests can assert on utils.CliWarning output.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String()
}

// parseSeqLte reads the optional seq__lte upper bound. An absent value means
// "no upper bound". An unparseable value is ignored here too, unlike the real
// server, whose strict integer filter rejects it — the CLI never sends one.
func parseSeqLte(r *http.Request) (to int, hasLte bool) {
	if lte := r.URL.Query().Get("seq__lte"); lte != "" {
		if v, err := strconv.Atoi(lte); err == nil {
			return v, true
		}
	}
	return 0, false
}

// holeServer serves /chunks/?seq__gte=N[&seq__lte=M] returning every seq in the
// window except those in `hole`. fetches counts the chunk requests. Used to
// simulate a seq that is never persisted server-side.
func holeServer(t *testing.T, maxSeq int, hole map[int]bool, fetches *atomic.Int32) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		from, _ := strconv.Atoi(r.URL.Query().Get("seq__gte"))
		to, hasLte := parseSeqLte(r)
		var results []Chunk
		for s := from; s <= maxSeq; s++ {
			if hasLte && s > to {
				break
			}
			if hole[s] {
				continue
			}
			results = append(results, Chunk{Seq: s, Content: fmt.Sprintf("c%d\n", s)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{Count: len(results), Results: results})
	}))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
}

// chunkServer serves an explicit chunk list (filtered by seq__gte and optional
// seq__lte), for tests that need specific or out-of-range seqs holeServer's
// contiguous range can't express.
func chunkServer(t *testing.T, chunks []Chunk) *client.AlpaconClient {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.Atoi(r.URL.Query().Get("seq__gte"))
		to, hasLte := parseSeqLte(r)
		var results []Chunk
		for _, c := range chunks {
			if c.Seq < from || (hasLte && c.Seq > to) {
				continue
			}
			results = append(results, c)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{Count: len(results), Results: results})
	}))
	t.Cleanup(ts.Close)
	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
}

func swapGapFillNow(fn func() time.Time) func() {
	old := gapFillNow
	gapFillNow = fn
	return func() { gapFillNow = old }
}

func swapMaxGapWidth(n int) func() {
	old := maxGapWidth
	maxGapWidth = n
	return func() { maxGapWidth = old }
}

func swapGapFillVars(initial time.Duration, factor int, max time.Duration, maxNoProgress int) func() {
	oi, of, om, on := gapFillInitialInterval, gapFillBackoffFactor, gapFillMaxInterval, gapFillMaxNoProgress
	gapFillInitialInterval, gapFillBackoffFactor, gapFillMaxInterval, gapFillMaxNoProgress = initial, factor, max, maxNoProgress
	return func() {
		gapFillInitialInterval, gapFillBackoffFactor, gapFillMaxInterval, gapFillMaxNoProgress = oi, of, om, on
	}
}

// While lastSeq is stuck, gapped chunks arriving within the backoff window must
// not each trigger a REST fetch.
func TestApplyChunk_ThrottlesRefetchWithinBackoffWindow(t *testing.T) {
	var fetches atomic.Int32
	ac := holeServer(t, 20, map[int]bool{1: true}, &fetches)

	now := time.Unix(0, 0)
	defer swapGapFillNow(func() time.Time { return now })()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	lastSeq := 0
	// Clock frozen: after the first (immediate) attempt, every further gapped
	// chunk is inside the window and must be skipped without a fetch.
	for seq := 2; seq <= 10; seq++ {
		lastSeq = applyChunk(ac, "cmd", lastSeq, ChunkEvent{Seq: seq, Content: fmt.Sprintf("c%d\n", seq)}, out, g)
	}

	assert.Equal(t, int32(1), fetches.Load(), "only the first gapped chunk should fetch; rest throttled")
	assert.Equal(t, 0, lastSeq, "lastSeq stays behind the permanent hole")
}

// After gapFillMaxNoProgress no-progress attempts, the missing seq is skipped:
// present chunks are printed, the hole is recorded in skipped, and lastSeq
// advances so live streaming resumes.
func TestApplyChunk_GivesUpAndSkipsPermanentGap(t *testing.T) {
	var fetches atomic.Int32
	ac := holeServer(t, 20, map[int]bool{1: true}, &fetches)

	defer swapGapFillVars(1*time.Millisecond, 2, 2*time.Millisecond, 3)()
	now := time.Unix(0, 0)
	defer swapGapFillNow(func() time.Time { return now })()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	lastSeq := 0
	for seq := 2; seq <= 6; seq++ {
		now = now.Add(10 * time.Millisecond) // advance past the window each time
		lastSeq = applyChunk(ac, "cmd", lastSeq, ChunkEvent{Seq: seq, Content: fmt.Sprintf("c%d\n", seq)}, out, g)
	}

	assert.LessOrEqual(t, fetches.Load(), int32(3), "fetches bounded by gapFillMaxNoProgress")
	assert.Equal(t, []int{1}, g.skipped, "seq 1 recorded as skipped")
	assert.Equal(t, 6, lastSeq, "streaming resumes past the skipped hole")
	assert.Equal(t, "c2\nc3\nc4\nc5\nc6\n", out.String(), "c1 lost, everything else printed in order")
}

// A gap that heals before the give-up limit resets the backoff and prints
// everything, with no seq recorded as skipped.
func TestApplyChunk_ResetsBackoffWhenGapHeals(t *testing.T) {
	var fetches atomic.Int32
	// No hole: the gap-fill fetch immediately returns the missing seq.
	ac := holeServer(t, 20, map[int]bool{}, &fetches)

	now := time.Unix(0, 0)
	defer swapGapFillNow(func() time.Time { return now })()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	lastSeq := 0
	lastSeq = applyChunk(ac, "cmd", lastSeq, ChunkEvent{Seq: 3, Content: "c3\n"}, out, g)

	assert.Equal(t, 3, lastSeq)
	assert.Equal(t, 0, g.noProgress, "progress resets the no-progress counter")
	assert.Empty(t, g.skipped)
	assert.Equal(t, "c1\nc2\nc3\n", out.String())
}

// A persistently failing gap-fill fetch must back off but never give up: the
// hole is only skipped once a successful fetch confirms it's absent, so a
// transient error can't drop output or warn "not arrived" for seqs that exist.
func TestApplyChunk_FetchErrorBacksOffButNeverGivesUp(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	defer swapGapFillVars(1*time.Millisecond, 2, 2*time.Millisecond, 3)()
	now := time.Unix(0, 0)
	defer swapGapFillNow(func() time.Time { return now })()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	lastSeq := 0
	stderr := captureStderr(t, func() {
		for seq := 2; seq <= 8; seq++ {
			now = now.Add(10 * time.Millisecond) // advance past the window each time
			lastSeq = applyChunk(ac, "cmd", lastSeq, ChunkEvent{Seq: seq, Content: fmt.Sprintf("c%d\n", seq)}, out, g)
		}
	})

	assert.Empty(t, g.skipped, "fetch errors never record a skip")
	assert.Equal(t, 0, lastSeq, "lastSeq stays behind the unfetchable hole")
	assert.Greater(t, g.noProgress, gapFillMaxNoProgress, "errors still back off past the give-up threshold")
	assert.Empty(t, out.String(), "no chunk delivered while the fetch keeps failing")
	assert.Contains(t, stderr, "failed to fetch missing chunks")
	assert.NotContains(t, stderr, "not arrived after", "fetch errors must not emit the give-up warning")
}

// Skipped seqs that got persisted late are recovered and printed at command
// end (out of order); no output is written for seqs still missing.
func TestRecoverSkippedChunks_RecoversLatePersistedSeq(t *testing.T) {
	var fetches atomic.Int32
	// seq 1 now exists (arrived late); seq 4 is still a permanent hole.
	ac := holeServer(t, 10, map[int]bool{4: true}, &fetches)

	g := &gapFillState{skipped: []int{1, 4}}
	out := &bytes.Buffer{}
	stderr := captureStderr(t, func() {
		g.recoverSkipped(ac, "cmd", out)
	})

	assert.Equal(t, "c1\n", out.String(), "recovered seq 1 printed; seq 4 stays missing")
	assert.Contains(t, stderr, "late chunk seq(s) [1] recovered at command end")
	assert.Contains(t, stderr, "chunk seq(s) [4] never arrived")
}

func TestRecoverSkippedChunks_NoopWhenNothingSkipped(t *testing.T) {
	var fetches atomic.Int32
	ac := holeServer(t, 10, map[int]bool{}, &fetches)

	g := &gapFillState{}
	out := &bytes.Buffer{}
	g.recoverSkipped(ac, "cmd", out)

	assert.Equal(t, int32(0), fetches.Load(), "no skipped seqs means no recovery fetch")
	assert.Empty(t, out.String())
}

// A permanent gap that was never given up on mid-stream (command finished
// before the backoff limit) must be surfaced by the terminal drain, not
// silently jumped over.
func TestDrainRemainingChunks_WarnsOnPermanentGap(t *testing.T) {
	var fetches atomic.Int32
	ac := holeServer(t, 8, map[int]bool{5: true}, &fetches)

	out := &bytes.Buffer{}
	var lastSeq int
	stderr := captureStderr(t, func() {
		lastSeq = drainRemainingChunks(ac, "cmd", 4, out)
	})

	assert.Equal(t, 8, lastSeq)
	assert.Equal(t, "c6\nc7\nc8\n", out.String(), "seq 5 missing, 6-8 still delivered")
	assert.Contains(t, stderr, "chunk seq(s) [5] never arrived")
}

// A hostile/buggy server can set a chunk seq arbitrarily high; giveUpGap must
// not enumerate the whole hole (which would spin the CLI and grow g.skipped
// without bound), yet still deliver the chunks it did fetch and resume past it.
func TestGiveUpGap_BoundsHugeServerSeq(t *testing.T) {
	g := &gapFillState{}
	out := &bytes.Buffer{}
	nextSeq := 1 << 30 // far above maxGapWidth; would hang if enumerated seq-by-seq (fits 32-bit int)
	fetched := []Chunk{{Seq: 100, Content: "kept\n"}}

	var last int
	stderr := captureStderr(t, func() {
		last = g.giveUpGap(0, nextSeq, fetched, out)
	})

	assert.Equal(t, nextSeq-1, last, "resumes just before the live chunk")
	assert.Empty(t, g.skipped, "oversized gap is not enumerated into skipped")
	assert.Equal(t, "kept\n", out.String(), "persisted chunks in the gap are still delivered")
	assert.Contains(t, stderr, "gap too large to recover")
}

// Many gaps each under maxGapWidth must not let g.skipped grow without bound
// across a long stream: cumulative recorded skips stay capped at maxGapWidth.
func TestGiveUpGap_BoundsCumulativeSkipped(t *testing.T) {
	defer swapMaxGapWidth(4)()

	g := &gapFillState{}
	out := &bytes.Buffer{}
	last := 0
	stderr := captureStderr(t, func() {
		for range 4 {
			next := last + 3 + 1 // gap of 3 seqs, each under maxGapWidth=4
			last = g.giveUpGap(last, next, nil, out)
		}
	})

	assert.Equal(t, maxGapWidth, len(g.skipped), "cumulative skipped capped at maxGapWidth")
	// Gap 1 is fully recorded (retryable), gap 2 fills the cap mid-gap (partial
	// retry), and later gaps must not claim a retry they won't get.
	assert.Contains(t, stderr, "will retry at command end")
	assert.Contains(t, stderr, "recorded 1 of 3 for retry at command end")
	assert.Contains(t, stderr, "skip budget exhausted, no retry")
}

// The terminal drain must bound its missing accumulator cumulatively too, while
// still delivering every chunk's content that the server did return.
func TestDrainRemainingChunks_BoundsCumulativeMissing(t *testing.T) {
	defer swapMaxGapWidth(2)()

	// Chunks at seq 3,6,9,12: the first 2-seq gap fills missing to the cap, then
	// the later gaps are summarized as truncated rather than enumerated.
	ac := chunkServer(t, []Chunk{
		{Seq: 3, Content: "c3\n"},
		{Seq: 6, Content: "c6\n"},
		{Seq: 9, Content: "c9\n"},
		{Seq: 12, Content: "c12\n"},
	})

	out := &bytes.Buffer{}
	var last int
	_ = captureStderr(t, func() {
		last = drainRemainingChunks(ac, "cmd", 0, out)
	})

	assert.Equal(t, 12, last)
	assert.Equal(t, "c3\nc6\nc9\nc12\n", out.String(), "all delivered chunks still printed")
}

// The terminal drain must likewise bound a server-supplied huge seq instead of
// enumerating every missing seq into the warning slice.
func TestDrainRemainingChunks_BoundsHugeServerSeq(t *testing.T) {
	huge := 1 << 30 // far above maxGapWidth; fits 32-bit int so 386 builds compile
	ac := chunkServer(t, []Chunk{{Seq: huge, Content: "x\n"}})

	out := &bytes.Buffer{}
	var last int
	stderr := captureStderr(t, func() {
		last = drainRemainingChunks(ac, "cmd", 0, out)
	})

	assert.Equal(t, huge, last)
	assert.Equal(t, "x\n", out.String(), "the delivered chunk is still printed")
	assert.Contains(t, stderr, "never arrived")
}

// A sub-maxGapWidth gap can still hold thousands of seqs; warnings must
// collapse them to a range instead of dumping the full slice.
func TestFormatSeqs_CollapsesLargeLists(t *testing.T) {
	assert.Equal(t, "[3 5 7]", formatSeqs([]int{3, 5, 7}))

	seqs := make([]int, 1000)
	for i := range seqs {
		seqs[i] = i + 10
	}
	assert.Equal(t, "10..1009 (1000 seqs)", formatSeqs(seqs))
}

func TestDrainRemainingChunks_NoWarnWhenContiguous(t *testing.T) {
	var fetches atomic.Int32
	ac := holeServer(t, 6, map[int]bool{}, &fetches)

	out := &bytes.Buffer{}
	var lastSeq int
	stderr := captureStderr(t, func() {
		lastSeq = drainRemainingChunks(ac, "cmd", 2, out)
	})

	assert.Equal(t, 6, lastSeq)
	assert.Equal(t, "c3\nc4\nc5\nc6\n", out.String())
	assert.NotContains(t, stderr, "never arrived")
}

// lteCapturingServer serves results and records the seq__gte/seq__lte query
// params of the last request, so a caller can assert the fetch was bounded.
// bounds() is read after the fetch completes, so the plain vars need no lock.
func lteCapturingServer(t *testing.T, results []Chunk) (ac *client.AlpaconClient, bounds func() (gte, lte string)) {
	t.Helper()
	var gte, lte string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gte = r.URL.Query().Get("seq__gte")
		lte = r.URL.Query().Get("seq__lte")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{Count: len(results), Results: results})
	}))
	t.Cleanup(ts.Close)
	ac = &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	return ac, func() (string, string) { return gte, lte }
}

// TestApplyChunk_SendsSeqLteBound verifies the gap-fill re-fetch is bounded by
// the live chunk that exposed the hole (seq__gte == lastSeq+1, seq__lte == chunk.Seq-1).
func TestApplyChunk_SendsSeqLteBound(t *testing.T) {
	// Return the whole gap so applyChunk can advance contiguously.
	ac, bounds := lteCapturingServer(t, []Chunk{{Seq: 1, Content: "c1\n"}, {Seq: 2, Content: "c2\n"}})

	g := &gapFillState{}
	out := &bytes.Buffer{}
	// lastSeq=0, live chunk seq=3 -> gap [1,2], bounds seq__gte=1, seq__lte=2.
	_ = applyChunk(ac, "cmd", 0, ChunkEvent{Seq: 3, Content: "c3\n"}, out, g)

	gte, lte := bounds()
	assert.Equal(t, "1", gte)
	assert.Equal(t, "2", lte)
}

// TestRecoverSkippedChunks_SendsSeqLteBound verifies the final recovery fetch is
// bounded by the first and last skipped seq (g.skipped is ascending).
func TestRecoverSkippedChunks_SendsSeqLteBound(t *testing.T) {
	ac, bounds := lteCapturingServer(t, nil)

	g := &gapFillState{skipped: []int{1, 4}}
	out := &bytes.Buffer{}
	_ = captureStderr(t, func() {
		g.recoverSkipped(ac, "cmd", out)
	})

	gte, lte := bounds()
	assert.Equal(t, "1", gte)
	assert.Equal(t, "4", lte)
}

// TestApplyChunk_OldServerIgnoringSeqLte_OutputIdentical verifies that when a
// server ignores seq__lte and returns the full tail, applyChunk's contiguous
// consumption still stops at the live chunk (client-side upper-bound filter).
func TestApplyChunk_OldServerIgnoringSeqLte_OutputIdentical(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Old server: ignores seq__lte, returns everything from seq__gte on.
		from, _ := strconv.Atoi(r.URL.Query().Get("seq__gte"))
		var results []Chunk
		for s := from; s <= 5; s++ {
			results = append(results, Chunk{Seq: s, Content: fmt.Sprintf("c%d\n", s)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{Count: len(results), Results: results})
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	g := &gapFillState{}
	out := &bytes.Buffer{}
	// Gap [1,2] exposed by live chunk seq=3; server also returns 4,5 (ignored).
	lastSeq := applyChunk(ac, "cmd", 0, ChunkEvent{Seq: 3, Content: "c3\n"}, out, g)

	assert.Equal(t, 3, lastSeq, "advances only through the live chunk, not the extra tail")
	assert.Equal(t, "c1\nc2\nc3\n", out.String(), "extra tail beyond the bound is not emitted")
}

// TestRecoverSkippedChunks_OldServerIgnoringSeqLte_OutputIdentical verifies that
// when a server ignores seq__lte and returns extra chunks, recoverSkipped emits
// only the skipped seqs it looks up via the bySeq map, never the extra tail.
func TestRecoverSkippedChunks_OldServerIgnoringSeqLte_OutputIdentical(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Old server: ignores seq__lte, returns everything from seq__gte on.
		from, _ := strconv.Atoi(r.URL.Query().Get("seq__gte"))
		var results []Chunk
		for s := from; s <= 6; s++ {
			results = append(results, Chunk{Seq: s, Content: fmt.Sprintf("c%d\n", s)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListResponse[Chunk]{Count: len(results), Results: results})
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	g := &gapFillState{skipped: []int{1, 4}}
	out := &bytes.Buffer{}
	// Server returns 1..6 (ignoring seq__lte=4); only skipped 1 and 4 are emitted.
	_ = captureStderr(t, func() {
		g.recoverSkipped(ac, "cmd", out)
	})

	assert.Equal(t, "c1\nc4\n", out.String(), "only skipped seqs emitted, extra tail ignored")
}
