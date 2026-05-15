package event

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/client"
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
	setPollConfig(t, 500*time.Millisecond, 2*time.Second, 50, 10*time.Millisecond)
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

			result, err := PollCommandExecution(ac, "cmd-1")
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

// newRunCommandBodyCaptureServer returns a test server that:
//   - responds to GET /api/servers/servers/?name=... with a 1-item server list
//   - captures the POST body for /api/events/commands/ and returns a minimal
//     CommandResponse list
//   - responds to PollCommandExecution's GET /api/events/commands/{id}/ with
//     a terminal status so RunCommand returns synchronously instead of
//     leaving a long-running goroutine behind.
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
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/events/commands/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(EventDetails{
				ID:     "cmd-1",
				Status: "completed",
				Result: "done",
			})
			return
		}
		http.NotFound(w, r)
	}))
}

// setPollConfig overrides polling timing variables for the duration of the test.
// Tests using this must not call t.Parallel() — package-level vars are not safe for concurrent mutation.
func setPollConfig(t *testing.T, idle, absolute time.Duration, maxErrors int, tick time.Duration) {
	t.Helper()
	origIdle, origAbsolute, origMaxErrors, origTick := pollIdleTimeout, pollAbsoluteTimeout, pollMaxConsecutiveErrors, pollTickInterval
	pollIdleTimeout = idle
	pollAbsoluteTimeout = absolute
	pollMaxConsecutiveErrors = maxErrors
	pollTickInterval = tick
	t.Cleanup(func() {
		pollIdleTimeout = origIdle
		pollAbsoluteTimeout = origAbsolute
		pollMaxConsecutiveErrors = origMaxErrors
		pollTickInterval = origTick
	})
}

// Case A: server always returns intermediate state → absolute timeout fires first.
func TestPollCommandExecution_AbsoluteTimeout(t *testing.T) {
	setPollConfig(t, 500*time.Millisecond, 100*time.Millisecond, 50, 10*time.Millisecond)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "running"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := PollCommandExecution(ac, "cmd-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute timeout")
}

// Case B: intermediate states keep resetting idle timer — no premature timeout.
func TestPollCommandExecution_IdleTimerReset(t *testing.T) {
	// idle timeout (50ms) is shorter than total sequence time, but each "running" resets it.
	setPollConfig(t, 50*time.Millisecond, 2*time.Second, 50, 10*time.Millisecond)

	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := "running"
		if int(reqCount.Add(1)) >= 5 {
			status = "completed"
		}
		_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: status, Result: "done"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	result, err := PollCommandExecution(ac, "cmd-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.Status)
}

// Case C: transient errors followed by success — function should recover without failing.
func TestPollCommandExecution_TransientErrorRecovery(t *testing.T) {
	setPollConfig(t, 500*time.Millisecond, 2*time.Second, 5, 10*time.Millisecond)

	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if int(reqCount.Add(1)) <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"detail":"service unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "completed", Result: "done"})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	result, err := PollCommandExecution(ac, "cmd-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.Status)
}

// Case D: consecutive errors reaching pollMaxConsecutiveErrors → fail with lastErr in message.
func TestPollCommandExecution_ConsecutiveErrorsFail(t *testing.T) {
	setPollConfig(t, 500*time.Millisecond, 2*time.Second, 3, 10*time.Millisecond)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := PollCommandExecution(ac, "cmd-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consecutive errors")
}

// Case E: terminal states (including stuck/error) are returned as-is — no error from PollCommandExecution.
func TestPollCommandExecution_TerminalStates(t *testing.T) {
	setPollConfig(t, 500*time.Millisecond, 2*time.Second, 5, 10*time.Millisecond)

	for _, status := range []string{"stuck", "error", "finished", "failed"} {
		t.Run(status, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: status})
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			result, err := PollCommandExecution(ac, "cmd-1")
			require.NoError(t, err)
			assert.Equal(t, status, result.Status)
		})
	}
}

// Case F: idle timeout fires after last intermediate state, with no further progress.
func TestPollCommandExecution_IdleTimeout(t *testing.T) {
	// idle=80ms, absolute=2s, maxErrors=50 (high so consecutive limit won't fire first)
	setPollConfig(t, 80*time.Millisecond, 2*time.Second, 50, 10*time.Millisecond)

	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if int(reqCount.Add(1)) == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "running"})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"server busy"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := PollCommandExecution(ac, "cmd-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idle timeout")
}

// Case G: absolute timeout and idle timeout produce distinguishable error messages.
func TestPollCommandExecution_TimeoutMessageDistinction(t *testing.T) {
	t.Run("absolute timeout message", func(t *testing.T) {
		setPollConfig(t, 500*time.Millisecond, 60*time.Millisecond, 50, 10*time.Millisecond)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(EventDetails{ID: "cmd-1", Status: "running"})
		}))
		defer ts.Close()
		ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
		_, err := PollCommandExecution(ac, "cmd-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "absolute timeout")
		assert.NotContains(t, err.Error(), "idle timeout")
	})

	t.Run("idle timeout message", func(t *testing.T) {
		setPollConfig(t, 60*time.Millisecond, 2*time.Second, 50, 10*time.Millisecond)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"detail":"server busy"}`))
		}))
		defer ts.Close()
		ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
		_, err := PollCommandExecution(ac, "cmd-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "idle timeout")
		assert.NotContains(t, err.Error(), "absolute timeout")
	})
}

// TestPollCommandExecution_MalformedJSON verifies that a well-formed HTTP 200 response
// with invalid JSON body returns an error immediately without retrying.
func TestPollCommandExecution_MalformedJSON(t *testing.T) {
	setPollConfig(t, 500*time.Millisecond, 2*time.Second, 5, 10*time.Millisecond)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	_, err := PollCommandExecution(ac, "cmd-1")
	require.Error(t, err)
}

func TestRunCommand_BodyIncludesWorkSession_WhenSet(t *testing.T) {
	var capture runCommandBodyCapture
	ts := newRunCommandBodyCaptureServer(t, &capture)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := RunCommand(ac, "server-x", "ls", "", "", nil, "ses-abc")
	require.NoError(t, err)

	hadKey, ws, _ := capture.snapshot()
	require.True(t, hadKey, "body must contain work_session field when ID is set")
	assert.Equal(t, "ses-abc", ws)
}

func TestRunCommand_BodyOmitsWorkSession_WhenEmpty(t *testing.T) {
	var capture runCommandBodyCapture
	ts := newRunCommandBodyCaptureServer(t, &capture)
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := RunCommand(ac, "server-x", "ls", "", "", nil, "")
	require.NoError(t, err)

	hadKey, _, _ := capture.snapshot()
	assert.False(t, hadKey, "body must omit work_session field when ID is empty")
}
