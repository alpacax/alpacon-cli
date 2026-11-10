package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	heldRequestUUID = "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47"
	heldStatusPath  = "/api/events/commands/cmd-1/"
	heldDeadline    = "2999-11-06T17:20:00+09:00"
)

var (
	reRunAction   = utils.NextAction{Command: "alpacon exec prod -- sudo reboot"}
	consoleAction = utils.NextAction{Description: "Approve it out of band in the Alpacon console (web/Slack). Where supported, pass --wait (or --wait-approval DURATION for a longer timeout) on the original command to block until approval."}
	heldActions   = []utils.NextAction{
		{Description: "Approval request: " + heldRequestUUID},
		{Description: "The approval must be decided by " + heldDeadline},
		{Command: "alpacon api " + heldStatusPath, Description: "Where to read the command's sudo_grant_status while waiting; once it reads authorized, re-run the command"},
	}
)

// newApprovalDenialServer returns a test server that resolves one server and
// always answers an exec command with the given plugin denial line
// (success=false), so the command stays pending.
func newApprovalDenialServer(denialLine string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			resp := map[string]any{
				"id":          "cmd-1",
				"status":      "completed",
				"success":     false,
				"exit_code":   1,
				"result":      denialLine,
				"error_phase": nil,
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

// newAwaitingApprovalServer returns a test server whose exec command parks at the
// server-side "awaiting_approval" status (HITL hold), so the command is pending
// human approval without ever producing a denial line.
func newAwaitingApprovalServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmd-1",
				"status": "awaiting_approval",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// pendingApprovalSignal is the machine-readable half of the pending-approval
// contract these tests assert on.
type pendingApprovalSignal struct {
	OK          bool               `json:"ok"`
	Status      string             `json:"status"`
	ExitCode    int                `json:"exit_code"`
	RequestID   string             `json:"request_id"`
	NextActions []utils.NextAction `json:"next_actions"`
}

type approvalWaitNotifier struct{ path string }

func (n approvalWaitNotifier) Notify(_ context.Context, title, _, _ string) error {
	f, err := os.OpenFile(n.path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintln(f, title)
	return err
}

// runExecHelper drives the real exec command in process against a fake server and returns its
// stdout, stderr, and exit code; the caller asserts the exit code it expects.
func runExecHelper(t *testing.T, workspaceURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runInProcess(t, workspaceURL, "", "", args)
}

// runExecLogsHelper is runExecHelper for `exec logs`, whose --output lives on the root command
// this tree does not build, so the format is set directly.
func runExecLogsHelper(t *testing.T, workspaceURL, outputFormat string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runInProcess(t, workspaceURL, "logs", outputFormat, args)
}

// runExecWaitHelper is runExecHelper with the approval-wait poll interval cut to
// milliseconds, so a test that drives the wait loop does not pay the 5s default
// per tick.
func runExecWaitHelper(t *testing.T, workspaceURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	orig := approvalWaitPollInterval
	t.Cleanup(func() { approvalWaitPollInterval = orig })
	approvalWaitPollInterval = 10 * time.Millisecond
	return runExecHelper(t, workspaceURL, args...)
}

// TestExecStatusAwaitingApprovalExits4WithJSONSignal drives the status-level HITL
// hold (server status "awaiting_approval", no denial line) through the real exec
// command and asserts it converges on the same pending-approval contract as the
// denial-code path: exit 4 and a {"status":"pending_approval", ...} envelope.
func TestExecStatusAwaitingApprovalExits4WithJSONSignal(t *testing.T) {
	ts := newAwaitingApprovalServer()
	defer ts.Close()

	stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "pending approval must exit 4")

	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec logs cmd-1", got.NextActions[0].Command, "hint should point at exec logs for the held job")
}

func TestExecPendingApprovalExits4WithJSONSignal(t *testing.T) {
	ts := newApprovalDenialServer(denialLine("SUDO_APPROVAL_REQUIRED"))
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "pending approval must exit 4")

	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.False(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
	require.NotEmpty(t, got.NextActions)
	assert.Equal(t, "alpacon exec prod -- sudo reboot", got.NextActions[0].Command, "re-run hint should reconstruct the invocation")

	// The pending message already says to re-run after approval, and this code
	// offers nothing past that wait, so printing its hint would say it twice.
	assert.NotContains(t, stderr, "Hint:")
}

// TestExecIntentDeviationPrintsSelfServiceHint pins the denial-code hint to the
// pending-approval path, which exits 4 before commandResultFailure. Without an
// explicit print, the reviewer-free way out of an intent deviation (re-declaring
// the session intent) never reaches the user.
func TestExecIntentDeviationPrintsSelfServiceHint(t *testing.T) {
	stdout, stderr := runIntentDeviationHelper(t)

	assert.Contains(t, stderr, "work-session update [SESSION_ID] --title")
	assert.Contains(t, stderr, "may need an approval of its own")

	// The hint rides on stderr, so the machine signal on stdout stays parseable.
	var got struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
}

// TestExecIntentDeviationPrintsSelfServiceHintAfterWaitTimeout covers the other
// way into pendingApprovalFailure: a --wait that ran out of window returns the
// last denial to the same handler, so the self-service hint must survive the
// wait rather than only reaching the user on the no-wait path.
func TestExecIntentDeviationPrintsSelfServiceHintAfterWaitTimeout(t *testing.T) {
	// Shorter than the poll interval, so the window closes on the timer without
	// the loop re-attempting the command.
	_, stderr := runIntentDeviationHelper(t, "--wait-approval", "1ms")

	assert.Contains(t, stderr, "Approval wait timed out")
	assert.Contains(t, stderr, "work-session update [SESSION_ID] --title")
}

// TestExecWaitTimeoutCarriesTheApprovalRequestID checks through a subprocess that the request id
// the wait loop picked up while polling reaches the JSON envelope, not only the in-process return
// value that TestRunExecWithApprovalWait_TimeoutCarriesTheApprovalRequestID checks.
func TestExecWaitTimeoutCarriesTheApprovalRequestID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                       "cmd-1",
				"status":                   "completed",
				"success":                  false,
				"exit_code":                1,
				"result":                   denialLine("SUDO_APPROVAL_REQUIRED"),
				"sudo_approval_request_id": "req-9",
				"sudo_grant_status":        "pending_approval",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	// Wide enough that the wait outlives a slow first poll: the request id only
	// reaches the envelope once a poll has read it, and every tick here pays a
	// real HTTP round trip inside a subprocess.
	stdout, _, exitCode := runExecWaitHelper(t, ts.URL, "--output", "json", "--wait-approval", "300ms", "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)

	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, "req-9", got.RequestID)
}

// runIntentDeviationHelper drives the real exec command against a server that
// answers with an intent-deviation denial, and returns its stdout and stderr
// after asserting the pending-approval exit code. extraArgs go before the
// server name.
func runIntentDeviationHelper(t *testing.T, extraArgs ...string) (stdout, stderr string) {
	t.Helper()

	ts := newApprovalDenialServer(denialLine("SUDO_INTENT_DEVIATION"))
	defer ts.Close()

	args := append([]string{"--output", "json"}, extraArgs...)
	args = append(args, "prod", "--", "sudo", "reboot")

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, args...)
	assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "pending approval must exit 4; stderr: %s", stderr)

	return stdout, stderr
}

// Exit 0 would tell a consumer reading the code alone that the command finished.
func TestExecLogsAwaitingApprovalExits4(t *testing.T) {
	const jobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+jobID+"/" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": jobID, "status": "awaiting_approval"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	t.Run("table", func(t *testing.T) {
		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, jobID)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode, "a held job is pending, not finished")
		assert.Contains(t, stderr, "Approval required")
	})

	t.Run("json", func(t *testing.T) {
		stdout, _, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, jobID)
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)

		var got pendingApprovalSignal
		require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
		assert.False(t, got.OK)
		assert.Equal(t, utils.PendingApprovalStatus, got.Status)
		assert.Equal(t, utils.ExitCodePendingApproval, got.ExitCode)
		require.NotEmpty(t, got.NextActions)
		assert.Equal(t, "alpacon exec logs "+jobID, got.NextActions[0].Command)
	})
}

// The sibling exit-6 paths (exec, work-session create) answer --output json with
// an error envelope, so prose here would break a consumer polling exec logs.
func TestExecLogsRejectedExits6(t *testing.T) {
	const jobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+jobID+"/" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": jobID, "status": "rejected", "rejection_gate": "approval"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	t.Run("table", func(t *testing.T) {
		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, jobID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "a rejection is final; exit 1 reads as retryable")
		assert.Contains(t, stderr, "was rejected; the server did not record why")
		assert.Contains(t, stderr, "==== ALPACON: COMMAND DENIED ====\n")
	})

	t.Run("json", func(t *testing.T) {
		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, jobID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode)

		var got struct {
			OK       bool   `json:"ok"`
			ExitCode int    `json:"exit_code"`
			Message  string `json:"message"`
		}
		require.NoError(t, json.Unmarshal([]byte(stderr), &got), "stderr: %s", stderr)
		assert.False(t, got.OK)
		assert.Equal(t, utils.ExitCodeNotApproved, got.ExitCode)
		assert.Contains(t, got.Message, "was rejected; the server did not record why")
	})
}

// Exit 1 reads as transient, so an agent re-runs a rejected command forever.
func TestExecRejectedExits6(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cmd-1", "status": "rejected"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "sudo", "reboot")
	assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
	assert.Contains(t, stderr, "Error: command cmd-1 was rejected; the server did not record why.\n")
}

// TestExecRejectedMidWaitExits6 traces the wait loop's rejection branch out to
// process exit: TestRunExecWithApprovalWait_RejectionMidWaitEndsTheWait stops at
// the returned error, and TestExecRejectedExits6 enters through the no-wait path.
func TestExecRejectedMidWaitExits6(t *testing.T) {
	var detailReads atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			// The first read is the submission's own completion poll: denied with
			// an approval request in flight, so --wait enters the loop. A reviewer
			// rejects it while the loop is waiting, seen on the next detail read.
			resp := map[string]any{
				"id":        "cmd-1",
				"status":    "completed",
				"success":   false,
				"exit_code": 1,
				"result":    denialLine("SUDO_APPROVAL_REQUIRED"),
			}
			if detailReads.Add(1) > 1 {
				resp["sudo_grant_status"] = "rejected"
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	_, stderr, exitCode := runExecWaitHelper(t, ts.URL, "--wait-approval", "30s", "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "a mid-wait rejection is final; stderr: %s", stderr)
	assert.Contains(t, stderr, "rejected by a reviewer")
}

func TestExecExpiredGrantSignalsOnlyNonLapseRejection(t *testing.T) {
	cases := []struct {
		name, cause, gate, message, hint string
		denied                           bool
	}{
		{
			name:    "risk denial",
			cause:   "risk_denied",
			gate:    "risk",
			message: "was rejected: the risk assessor refused it (rejection_cause: risk_denied, rejection_gate: risk)",
			hint:    "The workspace risk policy refuses this command; the same command is refused the same way.",
			denied:  true,
		},
		{
			name:    "no cause",
			gate:    "approval",
			message: "was not approved before the approval request expired",
		},
		{
			name:    "explicit lapse",
			cause:   "approval_expired",
			gate:    "approval",
			message: "was rejected: its approval request expired with no decision (rejection_cause: approval_expired, rejection_gate: approval)",
			hint:    "Ask an approver why the request went undecided.",
		},
	}
	for _, tc := range cases {
		for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				extra := map[string]any{"rejection_gate": tc.gate}
				if tc.cause != "" {
					extra["rejection_cause"] = tc.cause
				}
				ts, reads := newRejectedCommandSequenceServer("root", "wheel", extra, true, "expired")
				defer ts.Close()
				logPath := filepath.Join(t.TempDir(), "notifications")
				require.NoError(t, os.WriteFile(logPath, nil, 0600))

				origNotifier := localsignal.NewNotifier
				t.Cleanup(func() { localsignal.NewNotifier = origNotifier })
				localsignal.NewNotifier = func() localsignal.Notifier { return approvalWaitNotifier{path: logPath} }
				stdout, stderr, exitCode := runExecWaitHelper(t, ts.URL, "--output", format, "--wait-approval", "30s", "prod", "--", "sudo", "reboot")
				assert.Greater(t, reads.Load(), int64(2))
				assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
				assert.Equal(t, denialLine("SUDO_APPROVAL_REQUIRED"), stdout)
				notifications, err := os.ReadFile(logPath)
				require.NoError(t, err)
				wantDenied := 0
				if tc.denied {
					wantDenied = 1
				}
				assert.Equal(t, wantDenied, strings.Count(string(notifications), "Alpacon: command denied\n"), "DENIED notification calls")
				message := "command " + rejectedCommandID + " " + tc.message
				if tc.cause != "" {
					message += "."
				}
				if format == utils.OutputFormatTable {
					assert.Equal(t, wantDenied, strings.Count(stderr, "==== ALPACON: COMMAND DENIED ====\n"), "DENIED banners")
					assert.Contains(t, stderr, "Error: "+message+"\n")
					if tc.hint != "" {
						assert.Contains(t, stderr, "Hint: "+tc.hint+"\n")
					}
					return
				}
				envelope := lastEnvelope(t, stderr)
				assert.False(t, envelope.OK)
				assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
				code := tc.cause
				if code == "" {
					code = "approval_expired"
				}
				assert.Equal(t, code, envelope.ErrorCode)
				assert.Equal(t, message, envelope.Message)
			})
		}
	}
}

// newResultPollRefusalServer accepts the command but throttles every read of
// its result, so the result poll runs into its client timeout with that
// refusal last seen. Any other refusal would end the poll at once.
func newResultPollRefusalServer(hint string) *httptest.Server {
	return newResultPollServer(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
		body := map[string]any{"code": utils.APIRateLimited, "gate": "rate"}
		if hint != "" {
			body["hint"] = hint
		}
		_ = json.NewEncoder(w).Encode(body)
	})
}

// newResultPollServer accepts the command and answers every read of its result with result.
func newResultPollServer(result func(http.ResponseWriter)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			result(w)
		default:
			http.NotFound(w, r)
		}
	}))
}

// A result poll that times out stays a client timeout (exit 1, [client_timeout])
// even when the reads it timed out on were throttled: only the refusal's hint
// goes with the timeout, printed once, never its code.
func TestExecResultPollTimeoutStaysATimeout(t *testing.T) {
	const hint = "Too many requests were made in a short time; wait, then retry."
	for _, tt := range []struct {
		name string
		hint string
	}{{name: "with a hint", hint: hint}, {name: "without a hint"}} {
		t.Run(tt.name, func(t *testing.T) {
			ts := newResultPollRefusalServer(tt.hint)
			defer ts.Close()

			t.Setenv("ALPACON_EXEC_TIMEOUT", "300ms")
			_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "uptime")

			assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
			assert.Contains(t, stderr, "[client_timeout]")
			assert.NotContains(t, stderr, utils.APIRateLimited, "a timeout must not read as the throttle refusal")
			if tt.hint != "" {
				assert.Equal(t, 1, strings.Count(stderr, "Hint: "+hint), "stderr: %s", stderr)
				return
			}
			assert.NotContains(t, stderr, "Hint:")
		})
	}
}

// A poll that stops on a failed read leaves the command behind, so the report must
// not read as if it never ran: running it again could run it twice.
func TestExecResultPollFailureSaysTheCommandWasNotCancelled(t *testing.T) {
	ts := newResultPollServer(func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not found."}`))
	})
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "uptime")

	assert.Equal(t, 1, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "command cmd-1 was submitted and not cancelled")
	assert.Contains(t, stderr, "alpacon exec logs cmd-1")
	assert.NotContains(t, stderr, "failed to execute command")
}

func TestExecLogsApprovalLookupFallback(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		warning bool
	}{
		{name: "forbidden", status: http.StatusForbidden, body: `{}`},
		{name: "not found", status: http.StatusNotFound, body: `{}`},
		{name: "missing metadata", status: http.StatusOK, body: `{"results":[{"id":"apr-1"}]}`},
		{name: "missing command id", status: http.StatusOK, body: `{"results":[{"id":"apr-1","metadata":{}}]}`},
		{name: "server failure", status: http.StatusInternalServerError, body: `{}`, warning: true},
		{name: "invalid body", status: http.StatusOK, body: `{`, warning: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/events/commands/" + rejectedCommandID + "/":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": rejectedCommandID, "status": "awaiting_approval"})
				case "/api/approvals/approvals/-/":
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()

			_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, rejectedCommandID)
			assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
			assert.Contains(t, stderr, "awaiting human approval in the Alpacon console")
			assert.NotContains(t, stderr, "(approval request:")
			if tc.warning {
				assert.Contains(t, stderr, "failed to read approval request")
			} else {
				assert.NotContains(t, stderr, "failed to read approval request")
			}
		})
	}
}

// newHeldDenialServer answers an exec command with a sudo approval denial whose
// detail carries held, or no held key at all when held is nil.
func newHeldDenialServer(held map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			resp := map[string]any{
				"id":        "cmd-1",
				"status":    "completed",
				"success":   false,
				"exit_code": 1,
				"result":    denialLine("SUDO_APPROVAL_REQUIRED"),
			}
			if held != nil {
				resp["held"] = held
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

func fullHeld() map[string]any {
	return map[string]any{"approval_request": heldRequestUUID, "expires_at": heldDeadline, "status_path": heldStatusPath}
}

func TestExecPendingApproval_HeldReachesTheJSONEnvelope(t *testing.T) {
	ts := newHeldDenialServer(fullHeld())
	defer ts.Close()

	stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, heldRequestUUID, got.RequestID)
	assert.Equal(t, append([]utils.NextAction{reRunAction, consoleAction}, heldActions...), got.NextActions)
}

func TestExecPendingApproval_HeldIsPrintedInTableOutput(t *testing.T) {
	ts := newHeldDenialServer(fullHeld())
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	for _, action := range heldActions {
		assert.Contains(t, stderr, "  "+action.PlainText()+"\n")
	}
}

// An older server sends no held key; the envelope keeps the actions it had before.
func TestExecPendingApproval_NoHeldKeyKeepsTodaysOutput(t *testing.T) {
	ts := newHeldDenialServer(nil)
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
	var got pendingApprovalSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Empty(t, got.RequestID)
	assert.Equal(t, []utils.NextAction{reRunAction, consoleAction}, got.NextActions)
	assert.NotContains(t, stderr, "alpacon api")
}

func TestExecPendingApproval_BadStatusPathIsDropped(t *testing.T) {
	cases := []struct{ name, path string }{
		{"dot-dot segment", "/api/events/../servers/"},
		{"absolute URL", "https://evil.example.com/api/events/commands/cmd-1/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held := fullHeld()
			held["status_path"] = tc.path
			ts := newHeldDenialServer(held)
			defer ts.Close()

			stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")

			assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
			var got pendingApprovalSignal
			require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
			assert.Equal(t, append([]utils.NextAction{reRunAction, consoleAction}, heldActions[:2]...), got.NextActions)
		})
	}
}
