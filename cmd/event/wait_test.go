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
	newCommands := map[string]func(func() (*client.AlpaconClient, error)) *cobra.Command{opWait: newWaitCommand, opWatch: newWatchCommand}
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
			cmd := newCommands[tc.command](newClient)
			cmd.SetArgs(tc.args)
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
	cmd := newWaitCommand(newClient)
	cmd.SetArgs([]string{"--type", "notification", "--until", "created", "--timeout", "5s"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()

	exitErr, ok := errors.AsType[*utils.ExitError](err)
	require.True(t, ok, "want an ExitError, got %v", err)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
	assert.Eventually(t, func() bool { return len(waitGoroutines()) == 0 }, 2*time.Second, 10*time.Millisecond,
		"goroutines runWait started are still running: %v", waitGoroutines())
}

// newRejectingEventServer opens an event session and its WebSocket, then refuses the subscription,
// so Wait returns at once with the server's message.
func newRejectingEventServer(t *testing.T) *httptest.Server {
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
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"Invalid input."}`))
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

// waitGoroutines returns the stacks of live goroutines that runWait started.
func waitGoroutines() []string {
	buf := make([]byte, 1<<20)
	var found []string
	for g := range strings.SplitSeq(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if strings.Contains(g, "cmd/event.reportOutages") || strings.Contains(g, "cmd/event.runWait.func") {
			found = append(found, g)
		}
	}
	return found
}
