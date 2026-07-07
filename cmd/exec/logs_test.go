package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool    { return &b }
func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }

func TestIsRunningStatus(t *testing.T) {
	running := []string{"queued", "scheduled", "delivered", "verifying", "running", "acked"}
	for _, s := range running {
		assert.True(t, event.IsRunningStatus(s), "expected %q to be running", s)
	}
	terminal := []string{"completed", "success", "stuck", "error", "cancelled"}
	for _, s := range terminal {
		assert.False(t, event.IsRunningStatus(s), "expected %q to be terminal", s)
	}
}

func TestLogsCommandOutcome(t *testing.T) {
	tests := []struct {
		name               string
		details            event.EventDetails
		wantStdoutLine     string
		wantStderrContains []string
		wantStderrEmpty    bool
		wantExitCode       int
	}{
		{
			name:            "completed with result",
			details:         event.EventDetails{ID: "job-1", Status: "completed", Result: "Packages updated."},
			wantStdoutLine:  "Packages updated.",
			wantStderrEmpty: true,
			wantExitCode:    0,
		},
		{
			name:            "completed empty result",
			details:         event.EventDetails{ID: "job-1", Status: "completed", Result: ""},
			wantStdoutLine:  "",
			wantStderrEmpty: true,
			wantExitCode:    0,
		},
		{
			name:               "still running",
			details:            event.EventDetails{ID: "job-1", Status: "running"},
			wantStdoutLine:     "",
			wantStderrContains: []string{"still running", "status: running", "job-1"},
			wantExitCode:       0,
		},
		{
			name:               "queued",
			details:            event.EventDetails{ID: "job-2", Status: "queued"},
			wantStdoutLine:     "",
			wantStderrContains: []string{"still running", "status: queued", "job-2"},
			wantExitCode:       0,
		},
		{
			name:               "awaiting approval is informational, exit 0",
			details:            event.EventDetails{ID: "job-3", Status: "awaiting_approval"},
			wantStdoutLine:     "",
			wantStderrContains: []string{"awaiting approval", "status: awaiting_approval", "job-3"},
			wantExitCode:       0,
		},
		{
			name:               "rejected exits 5",
			details:            event.EventDetails{ID: "job-4", Status: "rejected"},
			wantStdoutLine:     "",
			wantStderrContains: []string{"rejected", "status: rejected", "do not retry"},
			wantExitCode:       utils.ExitCodeCommandRejected,
		},
		{
			name:               "stuck without phase",
			details:            event.EventDetails{ID: "job-1", Status: "stuck"},
			wantStdoutLine:     "",
			wantStderrContains: []string{"stuck"},
			wantExitCode:       1,
		},
		{
			name:               "stuck with agent_timeout phase",
			details:            event.EventDetails{ID: "job-1", Status: "stuck", ErrorPhase: strPtr("agent_timeout")},
			wantStdoutLine:     "",
			wantStderrContains: []string{"agent_timeout", "status=stuck"},
			wantExitCode:       1,
		},
		{
			name:               "cancelled",
			details:            event.EventDetails{ID: "job-1", Status: "cancelled"},
			wantStdoutLine:     "",
			wantStderrContains: []string{"cancelled"},
			wantExitCode:       1,
		},
		{
			name:               "cancelled with phase",
			details:            event.EventDetails{ID: "job-1", Status: "cancelled", ErrorPhase: strPtr("agent_disconnected")},
			wantStdoutLine:     "",
			wantStderrContains: []string{"agent_disconnected", "status=cancelled"},
			wantExitCode:       1,
		},
		{
			name: "error with agent_disconnected phase",
			details: event.EventDetails{
				ID:         "job-1",
				Status:     "error",
				ErrorPhase: strPtr("agent_disconnected"),
			},
			wantStdoutLine:     "",
			wantStderrContains: []string{"agent_disconnected", "status=error"},
			wantExitCode:       1,
		},
		{
			name: "remote failure exit 23",
			details: event.EventDetails{
				ID:       "job-1",
				Status:   "completed",
				Success:  boolPtr(false),
				ExitCode: intPtr(23),
				Result:   "partial transfer",
			},
			wantStdoutLine:  "partial transfer",
			wantStderrEmpty: true,
			wantExitCode:    23,
		},
		{
			name: "remote failure with phase",
			details: event.EventDetails{
				ID:         "job-1",
				Status:     "completed",
				Success:    boolPtr(false),
				ExitCode:   intPtr(124),
				ErrorPhase: strPtr("remote_command_exceeded_timeout"),
				Result:     "timed out",
			},
			wantStdoutLine:     "timed out",
			wantStderrContains: []string{"remote_command_exceeded_timeout"},
			wantExitCode:       124,
		},
		{
			name: "remote failure null exit code falls back to 1",
			details: event.EventDetails{
				ID:      "job-1",
				Status:  "completed",
				Success: boolPtr(false),
				Result:  "old alpamon output",
			},
			wantStdoutLine:  "old alpamon output",
			wantStderrEmpty: true,
			wantExitCode:    1,
		},
		{
			name:            "success status with nil Success",
			details:         event.EventDetails{ID: "job-1", Status: "success", Result: "ok"},
			wantStdoutLine:  "ok",
			wantStderrEmpty: true,
			wantExitCode:    0,
		},
		{
			name:               "unrecognised terminal status with nil Success exits 1",
			details:            event.EventDetails{ID: "job-1", Status: "denied"},
			wantStdoutLine:     "",
			wantStderrContains: []string{"unrecognised status", "denied"},
			wantExitCode:       1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdoutLine, stderrLine, exitCode := logsCommandOutcome(tt.details)

			assert.Equal(t, tt.wantStdoutLine, stdoutLine, "stdout line")
			assert.Equal(t, tt.wantExitCode, exitCode, "exit code")

			if tt.wantStderrEmpty {
				assert.Empty(t, stderrLine, "stderr should be empty")
			} else {
				for _, sub := range tt.wantStderrContains {
					assert.Contains(t, stderrLine, sub, "stderr should contain %q", sub)
				}
				if len(stderrLine) > 0 {
					assert.Equal(t, '\n', rune(stderrLine[len(stderrLine)-1]), "stderr should end with newline")
				}
			}
		})
	}
}

// TestBuildLogsJSON drives buildLogsJSON with one representative case per
// server_status group from spec section 3.4.1, and asserts the resulting
// envelope's status/exit_code pairing matches that table exactly.
func TestBuildLogsJSON(t *testing.T) {
	tests := []struct {
		name           string
		details        event.EventDetails
		wantStatus     string
		wantExitCode   int
		wantErrorCode  string
		wantErrorPhase string
		wantResult     string
		wantNextAction bool
	}{
		{
			name:           "running group: queued",
			details:        event.EventDetails{ID: "job-1", Status: "queued"},
			wantStatus:     "running",
			wantExitCode:   0,
			wantNextAction: true,
		},
		{
			name:           "pending_approval group: awaiting_approval",
			details:        event.EventDetails{ID: "job-2", Status: "awaiting_approval"},
			wantStatus:     utils.PendingApprovalStatus,
			wantExitCode:   0,
			wantNextAction: true,
		},
		{
			name:          "rejected group",
			details:       event.EventDetails{ID: "job-3", Status: "rejected"},
			wantStatus:    utils.RejectedStatus,
			wantExitCode:  utils.ExitCodeCommandRejected,
			wantErrorCode: "command_rejected",
		},
		{
			name: "failed group: stuck with phase",
			details: event.EventDetails{
				ID:         "job-4",
				Status:     "stuck",
				ErrorPhase: strPtr("agent_timeout"),
			},
			wantStatus:     "failed",
			wantExitCode:   1,
			wantErrorPhase: "agent_timeout",
		},
		{
			name: "failed group: remote non-zero exit",
			details: event.EventDetails{
				ID:       "job-5",
				Status:   "completed",
				Success:  boolPtr(false),
				ExitCode: intPtr(23),
				Result:   "partial transfer",
			},
			wantStatus:   "failed",
			wantExitCode: 23,
			wantResult:   "partial transfer",
		},
		{
			name:         "succeeded group",
			details:      event.EventDetails{ID: "job-6", Status: "completed", Result: "ok"},
			wantStatus:   "succeeded",
			wantExitCode: 0,
			wantResult:   "ok",
		},
		{
			name:         "unknown group: unrecognised status",
			details:      event.EventDetails{ID: "job-7", Status: "denied"},
			wantStatus:   "unknown",
			wantExitCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdoutLine, stderrLine, exitCode := logsCommandOutcome(tt.details)
			envelope := buildLogsJSON(tt.details, stdoutLine, stderrLine, exitCode)

			assert.True(t, envelope.OK, "ok is always true: the query itself succeeded")
			assert.Equal(t, tt.wantStatus, envelope.Status)
			assert.Equal(t, tt.details.Status, envelope.ServerStatus)
			assert.Equal(t, tt.wantExitCode, envelope.ExitCode)
			assert.Equal(t, exitCode, envelope.ExitCode, "JSON exit_code must match the plain-text exit code")
			assert.Equal(t, tt.wantErrorCode, envelope.ErrorCode)
			assert.Equal(t, tt.wantErrorPhase, envelope.ErrorPhase)
			assert.Equal(t, tt.wantResult, envelope.Result)
			assert.Equal(t, tt.details.ID, envelope.JobID)
			assert.NotContains(t, envelope.Message, "\x1b", "message must have ANSI codes stripped")
			if tt.wantNextAction {
				assert.NotEmpty(t, envelope.NextActions)
			} else {
				assert.Empty(t, envelope.NextActions)
			}
		})
	}
}

// newLogsDetailServer returns a test server that answers GET
// /api/events/commands/{jobID}/ with details. The chunks endpoint (used by
// GetCommandOutput) is deliberately left unhandled: logs.go's Run tolerates
// that fetch failing and falls back to details.Result.
func newLogsDetailServer(t *testing.T, jobID string, details map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+jobID+"/" {
			_ = json.NewEncoder(w).Encode(details)
			return
		}
		http.NotFound(w, r)
	}))
}

// logsJSONHelperArgs finds the "exec-logs-json-helper" marker in os.Args and
// returns everything after it, mirroring execWorkSessionHelperArgs.
func logsJSONHelperArgs(args []string) ([]string, bool) {
	for i := range args {
		if args[i] == "exec-logs-json-helper" {
			return args[i+1:], true
		}
	}
	return nil, false
}

// TestExecLogsJSONHelperProcess is re-invoked as a subprocess (via os.Args[0])
// by the integration tests below; it is a no-op under a normal `go test` run.
//
// logsCmd has no DisableFlagParsing (unlike ExecCmd/WebshCmd) and relies on
// Cobra's normal flag parsing to strip the --output persistent flag, which is
// only wired up by going through RootCmd.Execute() (package cmd, which cannot
// be imported here without a cycle). Set utils.OutputFormat directly instead,
// mirroring cmd/worksession/worksession_error_json_test.go's helper process,
// and hand logsCmd.Run only the positional JOB_ID it actually parses itself.
func TestExecLogsJSONHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_LOGS_JSON_HELPER") != "1" {
		return
	}

	args, ok := logsJSONHelperArgs(os.Args)
	if !ok {
		fmt.Fprintln(os.Stderr, "missing exec-logs-json-helper marker")
		os.Exit(2)
	}
	utils.OutputFormat = utils.OutputFormatJSON
	logsCmd.Run(logsCmd, args)
	// logsCmd.Run only calls os.Exit for a non-zero exit code; without an
	// explicit exit here, a zero-exit-code run (e.g. pending) falls through to
	// `go test`'s own completion output ("PASS\n"), corrupting the captured
	// stdout JSON.
	os.Exit(0)
}

func runLogsJSONHelper(t *testing.T, jobID, workspaceURL string) (stdout, stderr bytes.Buffer, err error) {
	t.Helper()
	home := t.TempDir()
	writeExecCommandTestConfig(t, home, workspaceURL)

	helper := osexec.Command(
		os.Args[0],
		"-test.run=^TestExecLogsJSONHelperProcess$",
		"--",
		"exec-logs-json-helper",
		jobID,
	)
	helper.Env = append(os.Environ(),
		"GO_WANT_EXEC_LOGS_JSON_HELPER=1",
		"HOME="+home,
	)
	helper.Stdout = &stdout
	helper.Stderr = &stderr
	err = helper.Run()
	return stdout, stderr, err
}

// TestExecLogsJSONPending drives a job parked at "awaiting_approval" through
// the real `exec logs --output json` command and asserts the pending row of
// spec section 3.4.1: exit 0 (breaking change avoided) and a
// {"status":"pending_approval", ...} envelope (AC8).
func TestExecLogsJSONPending(t *testing.T) {
	const jobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"
	ts := newLogsDetailServer(t, jobID, map[string]any{
		"id":     jobID,
		"status": "awaiting_approval",
	})
	defer ts.Close()

	stdout, stderr, err := runLogsJSONHelper(t, jobID, ts.URL)
	require.NoError(t, err, "pending must exit 0: stderr=%s", stderr.String())

	var got logsJSONEnvelope
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.True(t, got.OK)
	assert.Equal(t, utils.PendingApprovalStatus, got.Status)
	assert.Equal(t, "awaiting_approval", got.ServerStatus)
	assert.Equal(t, 0, got.ExitCode)
	assert.Equal(t, jobID, got.JobID)
	require.NotEmpty(t, got.NextActions)
}

// TestExecLogsJSONRejected drives a job in the terminal "rejected" status
// through `exec logs --output json` and asserts exit 5 plus the rejected
// envelope (AC3 exit code applied consistently to the JSON path).
func TestExecLogsJSONRejected(t *testing.T) {
	const jobID = "b2c3d4e5-6789-bcde-f012-3456789abcde"
	ts := newLogsDetailServer(t, jobID, map[string]any{
		"id":     jobID,
		"status": "rejected",
	})
	defer ts.Close()

	stdout, stderr, err := runLogsJSONHelper(t, jobID, ts.URL)
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T: stderr=%s", err, stderr.String())
	assert.Equal(t, utils.ExitCodeCommandRejected, exitErr.ExitCode())

	var got logsJSONEnvelope
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.True(t, got.OK)
	assert.Equal(t, utils.RejectedStatus, got.Status)
	assert.Equal(t, utils.ExitCodeCommandRejected, got.ExitCode)
	assert.Equal(t, "command_rejected", got.ErrorCode)
}

// TestExecLogsJSONFailed drives a job stuck with an error phase through
// `exec logs --output json` and asserts the failed row of spec section 3.4.1:
// exit code and error_phase match the existing logsCommandOutcome logic (AC11).
func TestExecLogsJSONFailed(t *testing.T) {
	const jobID = "c3d4e5f6-789a-cdef-0123-456789abcdef"
	ts := newLogsDetailServer(t, jobID, map[string]any{
		"id":          jobID,
		"status":      "stuck",
		"error_phase": "agent_timeout",
	})
	defer ts.Close()

	stdout, stderr, err := runLogsJSONHelper(t, jobID, ts.URL)
	require.Error(t, err)
	var exitErr *osexec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected child process exit error, got %T: stderr=%s", err, stderr.String())
	assert.Equal(t, 1, exitErr.ExitCode())

	var got logsJSONEnvelope
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), "stdout: %s", stdout.String())
	assert.True(t, got.OK)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "stuck", got.ServerStatus)
	assert.Equal(t, 1, got.ExitCode)
	assert.Equal(t, "agent_timeout", got.ErrorPhase)
}
