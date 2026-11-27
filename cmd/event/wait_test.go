package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEventTargetAllowsOmittedTarget(t *testing.T) {
	t.Parallel()

	target, err := validateEventTarget("")

	require.NoError(t, err)
	assert.Empty(t, target)
}

func TestValidateEventTargetRejectsInvalidID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
	}{
		{name: "whitespace only", target: "   "},
		{name: "parent traversal", target: "../x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := validateEventTarget(tc.target)

			require.ErrorIs(t, err, utils.ErrInvalidID)
			assert.Empty(t, got)
		})
	}
}

func TestValidateEventTargetReturnsTrimmedID(t *testing.T) {
	t.Parallel()

	target, err := validateEventTarget("  session-id  ")

	require.NoError(t, err)
	assert.Equal(t, "session-id", target)
}

func TestWaitAndWatch_RefuseWhatTheyCannotRun(t *testing.T) {
	t.Parallel()
	newCommands := map[string]func(func() (*client.AlpaconClient, error), *utils.IOStreams) *cobra.Command{opWait: newWaitCommand, opWatch: newWatchCommand}
	cases := []struct {
		name       string
		command    string
		args       []string
		wantCode   int
		wantLine   string
		connects   bool // the client factory succeeds
		wantClient bool
	}{
		{name: "wait invalid target", command: opWait, args: []string{"--type", "command", "--target", "../x"}, wantCode: utils.ExitCodeUsageError, wantLine: "invalid target ID"},
		{name: "watch invalid target", command: opWatch, args: []string{"--type", "command", "--target", "../x"}, wantCode: utils.ExitCodeUsageError, wantLine: "invalid target ID"},
		{name: "wait without a type", command: opWait, wantCode: utils.ExitCodeUsageError, wantLine: "--type is required."},
		{name: "watch without a type", command: opWatch, wantCode: utils.ExitCodeUsageError, wantLine: "--type is required."},
		{name: "wait with a blank until", command: opWait, args: []string{"--type", "work_session", "--until", " , "}, wantCode: utils.ExitCodeUsageError, wantLine: "--until needs at least one sub type."},
		{name: "wait with a zero timeout", command: opWait, args: []string{"--type", "work_session", "--timeout", "0s"}, wantCode: utils.ExitCodeUsageError, wantLine: "--timeout must be positive."},
		{name: "wait for an unknown type without until", command: opWait, args: []string{"--type", "notification"}, wantCode: utils.ExitCodeUsageError, wantLine: "--until is required for notification", connects: true, wantClient: true},
		{name: "wait without a client", command: opWait, args: []string{"--type", "work_session"}, wantCode: utils.ExitCodeGeneralError, wantLine: "Connection to Alpacon API failed: no client. Consider re-logging.", wantClient: true},
		{name: "watch without a client", command: opWatch, args: []string{"--type", "work_session"}, wantCode: utils.ExitCodeGeneralError, wantLine: "Connection to Alpacon API failed: no client. Consider re-logging.", wantClient: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clientCalled := false
			newClient := func() (*client.AlpaconClient, error) {
				clientCalled = true
				if tc.connects {
					return &client.AlpaconClient{}, nil
				}
				return nil, errors.New("no client")
			}
			cmd := newCommands[tc.command](newClient, utils.Stdio)
			cmd.SetArgs(append([]string{}, tc.args...)) // never nil, or Cobra reads os.Args[1:]
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			err := cmd.Execute()

			exitErr, ok := errors.AsType[*utils.ExitError](err)
			require.True(t, ok, "want an ExitError, got %v", err)
			assert.Equal(t, tc.wantCode, exitErr.Code)
			assert.Equal(t, tc.wantClient, clientCalled)
			assert.Empty(t, stdout.String())
			assert.Empty(t, stderr.String(), "cobra must leave the failure to cmd.Execute")
			var printed bytes.Buffer
			utils.FprintExitError(&printed, exitErr)
			assert.Contains(t, printed.String(), tc.wantLine)
		})
	}
}

// Serial: it reads every goroutine's stack, so a parallel neighbor's runWait would count.
func TestRunWait_LeavesNoGoroutineBehindOnceItReturns(t *testing.T) {
	ts := newRejectingEventServer(t)
	newClient := func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, nil
	}
	cmd := newWaitCommand(newClient, utils.Stdio)
	cmd.SetArgs([]string{"--type", "notification", "--until", "created", "--timeout", "5s"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()

	exitErr, ok := errors.AsType[*utils.ExitError](err)
	require.True(t, ok, "want an ExitError, got %v", err)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
	assert.Empty(t, waitGoroutines(false), "joined before runWait returns")
	assert.Eventually(t, func() bool { return len(waitGoroutines(true)) == 0 }, 2*time.Second, 10*time.Millisecond,
		"the signal goroutine is still running")
}

func TestRunWait_JSONEndsARefusedWaitWithOneEnvelope(t *testing.T) {
	t.Parallel()
	const sessionID = "550e8400-e29b-41d4-a716-446655440000"
	ts := newEventServer(t, http.StatusCreated, `{}`)
	ts.Config.Handler.(*http.ServeMux).HandleFunc("GET /api/work-sessions/sessions/"+sessionID+"/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + sessionID + `","status":"rejected"}`))
	})
	newClient := func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, nil
	}

	stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command { return newWaitCommand(newClient, ios) },
		"--type", "work_session", "--target", sessionID, "--timeout", "30s")

	assert.Equal(t, utils.ExitCodeNotApproved, code)
	assert.Contains(t, stdout, "rejected")
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	require.Len(t, lines, 2, "want the waiting notice and one envelope: %q", stderr)
	assert.JSONEq(t, `{"ok":false,"exit_code":6,"error_code":"not_approved","message":"the wait ended on a work_session event in its failure set","context":{"operation":"wait"}}`, lines[1])
}

// newRejectingEventServer opens an event session and its WebSocket, then refuses the subscription,
// so Wait returns at once with the server's message.
func newRejectingEventServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newEventServer(t, http.StatusBadRequest, `{"detail":"Invalid input."}`)
}

// newEventServer opens an event session and its WebSocket, then answers the subscription with status and body.
func newEventServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	mux.HandleFunc("POST /api/events/sessions/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "session", "websocket_url": "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/", "channel_id": "channel"})
	})
	mux.HandleFunc("DELETE /api/events/sessions/{id}/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/events/subscriptions/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	upgrader := websocket.Upgrader{}
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	return ts
}

// waitGoroutines returns the stacks of live goroutines runWait started: with signal, the signal
// goroutine, which ends on its own once runWait returns; without, the reporter and every
// goroutine of the waiter, which runWait joins.
func waitGoroutines(signal bool) []string {
	buf := make([]byte, 1<<20)
	var found []string
	for g := range strings.SplitSeq(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		isSignal := strings.Contains(g, "cmd/event.runWait.func") && !strings.Contains(g, "reportOutages")
		joined := strings.Contains(g, "cmd/event.reportOutages") || strings.Contains(g, "alpacon-cli/api/event.")
		if (signal && isSignal) || (!signal && joined) {
			found = append(found, g)
		}
	}
	return found
}

// event wait has no --wait flag, so its exit 4 must not suggest one.
func TestRunWait_TimeoutNamesRunningTheWaitAgain(t *testing.T) {
	t.Parallel()
	const sessionID = "550e8400-e29b-41d4-a716-446655440000"
	ts := newEventServer(t, http.StatusCreated, `{}`)
	ts.Config.Handler.(*http.ServeMux).HandleFunc("GET /api/work-sessions/sessions/"+sessionID+"/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + sessionID + `","status":"pending"}`))
	})
	newClient := func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, nil
	}

	_, stderr, code := cmdtest.RunCaptured(func(ios *utils.IOStreams) *cobra.Command { return newWaitCommand(newClient, ios) },
		"--type", "work_session", "--target", sessionID, "--timeout", "1s")

	assert.Equal(t, utils.ExitCodePendingApproval, code)
	assert.Contains(t, stderr, "  run the same command again to keep waiting, or raise --timeout\n")
	assert.NotContains(t, stderr, "on the original command")
}
