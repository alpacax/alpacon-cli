package exec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientTimeoutLine(t *testing.T) {
	t.Parallel()
	line := clientTimeoutLine()
	assert.Contains(t, line, "[client_timeout]", "stderr should carry the phase id in brackets")
	assert.Contains(t, line, event.DescribePhase("client_timeout"),
		"stderr should include the human-readable description")
	assert.True(t, strings.HasSuffix(line, "\n"), "line must end with a newline: %q", line)
}

func TestAsPhasedError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		err     error
		wantOk  bool
		wantNil bool
	}{
		{name: "nil_returns_false", err: nil, wantOk: false, wantNil: true},
		{name: "remote_command_error", err: &event.RemoteCommandError{ExitCode: 23}, wantOk: true},
		{name: "client_timeout", err: &event.ClientTimeoutError{}, wantOk: true},
		{name: "wrapped_remote_command_error", err: fmt.Errorf("wrap: %w", &event.RemoteCommandError{ExitCode: 1}), wantOk: true},
		{name: "wrapped_client_timeout", err: fmt.Errorf("wrap: %w", &event.ClientTimeoutError{}), wantOk: true},
		{name: "plain_error", err: errors.New("nope"), wantOk: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asPhasedError(tt.err)
			assert.Equal(t, tt.wantOk, ok)
			if tt.wantNil {
				assert.NoError(t, got)
			}
		})
	}
}

func TestRemoteCommandOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		remoteErr        *event.RemoteCommandError
		wantStderrEmpty  bool
		wantStderrPhrase string
		wantExitCode     int
	}{
		{
			name: "phase_propagates_exit_124",
			remoteErr: &event.RemoteCommandError{
				Output:     "command timed out",
				ExitCode:   124,
				ErrorPhase: "remote_command_exceeded_timeout",
			},
			wantStderrPhrase: "remote_command_exceeded_timeout",
			wantExitCode:     124,
		},
		{
			name: "non_zero_exit_without_phase_skips_stderr_line",
			remoteErr: &event.RemoteCommandError{
				Output:     "rsync: some files vanished",
				ExitCode:   23,
				ErrorPhase: "",
			},
			wantStderrEmpty: true,
			wantExitCode:    23,
		},
		{
			name: "phase_still_emits_stderr",
			remoteErr: &event.RemoteCommandError{
				ExitCode:   1,
				ErrorPhase: "agent_timeout",
			},
			wantStderrPhrase: "agent_timeout",
			wantExitCode:     1,
		},
		{
			name: "exit_1_no_phase",
			remoteErr: &event.RemoteCommandError{
				ExitCode:   1,
				ErrorPhase: "",
			},
			wantStderrEmpty: true,
			wantExitCode:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderrLine, exitCode := remoteCommandOutcome(tt.remoteErr)

			assert.Equal(t, tt.wantExitCode, exitCode, "exit code should match")

			if tt.wantStderrEmpty {
				assert.Empty(t, stderrLine, "stderr line should be empty when no phase")
				return
			}
			assert.Contains(t, stderrLine, fmt.Sprintf("[%s]", tt.wantStderrPhrase),
				"stderr should carry the phase identifier in brackets for CI/grep")
			assert.Contains(t, stderrLine, event.DescribePhase(tt.wantStderrPhrase),
				"stderr should include the human-readable phase description")
			assert.True(t, strings.HasSuffix(stderrLine, "\n"),
				"stderr line must end with a newline: %q", stderrLine)
		})
	}
}

// The phase line bypasses the Cli* helpers, and DescribePhase echoes an unknown
// phase verbatim, so an attacker-chosen error_phase must be sanitized where the
// line is assembled (#364). The second case pins the order: the lookup runs on
// the raw phase, so a payload that only becomes a known phase after sanitizing
// still renders as an identifier and cannot borrow that phase's description.
func TestRemoteCommandOutcomeSanitizesPhase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		phase      string
		wantStderr string
	}{
		{
			name:       "unknown phase keeps its safe characters",
			phase:      "boom\x1b[2K\u202e_phase",
			wantStderr: utils.Red("Error") + ": [boom_phase] boom_phase\n",
		},
		{
			// Describing before stripping is what keeps the payload from borrowing
			// agent_timeout's own description once it sanitizes into that phase.
			name:       "payload sanitizing into a known phase does not borrow its description",
			phase:      "agent_\x1b[2K\u202etimeout",
			wantStderr: utils.Red("Error") + ": [agent_timeout] agent_timeout\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderrLine, exitCode := remoteCommandOutcome(&event.RemoteCommandError{
				ExitCode:   1,
				ErrorPhase: tt.phase,
			})

			assert.Equal(t, 1, exitCode)
			assert.Equal(t, tt.wantStderr, stderrLine)
		})
	}
}

func TestDetachResultLines(t *testing.T) {
	t.Parallel()
	line1, line2 := detachResultLines("a1b2c3d4-1234-5678-abcd-000000000000")
	assert.Equal(t, "Job submitted: a1b2c3d4-1234-5678-abcd-000000000000", line1)
	assert.Equal(t, "Run `alpacon exec logs a1b2c3d4-1234-5678-abcd-000000000000` to check the result.", line2)
}

// The job id comes from the server's submit response and both lines are written
// raw (stdout and stderr), outside the Cli* helpers—same class as #364.
func TestDetachResultLinesSanitizesJobID(t *testing.T) {
	t.Parallel()
	line1, line2 := detachResultLines("job-1\x1b[2K\u202e")

	assert.Equal(t, "Job submitted: job-1", line1)
	assert.Equal(t, "Run `alpacon exec logs job-1` to check the result.", line2)
}

// stubApprovalWaitSeams swaps the loop's seams/interval (restored on cleanup) and returns a denial carrying the plugin line the loop keys on for that code.
// CommandID is set to "cmd-1": the wait loop polls the command detail by id, so
// a denial with no id would never enter the loop at all.
func stubApprovalWaitSeams(t *testing.T, interval time.Duration, code string) *event.RemoteCommandError {
	t.Helper()
	origStepUp, origStream, origInterval, origGetCommand := runPresenceStepUp, streamApprovedCommand, approvalWaitPollInterval, getCommandByID
	t.Cleanup(func() {
		runPresenceStepUp, streamApprovedCommand, approvalWaitPollInterval, getCommandByID = origStepUp, origStream, origInterval, origGetCommand
	})
	approvalWaitPollInterval = interval
	return &event.RemoteCommandError{Output: denialLine(code), ExitCode: 1, CommandID: "cmd-1"}
}

func TestRunExecWithApprovalWait_PollsTheCommandDetailInsteadOfResubmitting(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")

	submits := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		submits++
		if submits == 1 {
			return denial
		}
		return nil
	}

	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		status := "pending_approval"
		if polls >= 3 {
			status = "authorized"
		}
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "cmd", "", "", nil, "", "", time.Second, io.Discard)

	require.NoError(t, err)
	assert.Equal(t, 2, submits, "one first attempt and one run after approval")
	assert.Equal(t, 3, polls)
}

func TestRunExecWithApprovalWait_RejectionEndsTheWaitWithoutAGrant(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{"rejected", "rejected"},
		{"expired", "expired"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")

			runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
				return denial
			}
			status := tt.status
			getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
				return event.EventDetails{SudoGrantStatus: &status}, nil
			}

			err := RunExecWithApprovalWait(nil, "srv", "cmd", "", "", nil, "", "", time.Minute, io.Discard)

			var rejected *event.CommandRejectedError
			assert.ErrorAs(t, err, &rejected)
		})
	}
}

func TestRunExecWithApprovalWait_SecondDenialDoesNotOpenAnotherWait(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")

	submits := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		submits++
		return denial
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		status := "authorized"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}

	stderr := captureStderr(t, func() {
		err := RunExecWithApprovalWait(nil, "srv", "cmd", "", "", nil, "", "", time.Minute, io.Discard)
		assert.ErrorIs(t, err, denial)
	})

	assert.Equal(t, 2, submits, "the wait must not re-enter after the post-approval run")
	assert.Equal(t, 1, polls)
	assert.Contains(t, stderr, "already used")
}

func TestRunExecWithApprovalWait_TimeoutCarriesTheApprovalRequestID(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")

	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		status := "pending_approval"
		requestID := "req-9"
		return event.EventDetails{SudoGrantStatus: &status, SudoApprovalRequestID: &requestID}, nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "cmd", "", "", nil, "", "", 20*time.Millisecond, io.Discard)

	var remoteErr *event.RemoteCommandError
	require.ErrorAs(t, err, &remoteErr)
	assert.Equal(t, "req-9", remoteErr.ApprovalRequestID)
}

// TestRunExecWithApprovalWait_EntersLoopOnIntentDeviation pins that an intent
// deviation denial (the same HITL branch server-side as SUDO_APPROVAL_REQUIRED,
// sudo/services.py, with only the code swapped) enters the poll loop rather than
// returning the first denial, and that a grant reaching the polled command
// detail runs the command once more.
func TestRunExecWithApprovalWait_EntersLoopOnIntentDeviation(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_INTENT_DEVIATION")

	submits := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		submits++
		if submits == 1 {
			return denial
		}
		return nil // a reviewer approved it; the post-approval run succeeds
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		status := "pending_approval"
		if polls >= 2 {
			status = "authorized"
		}
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("a denial-code wait polls the command detail; it never resumes a held job")
		return nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", time.Second, io.Discard)

	require.NoError(t, err)
	assert.Equal(t, 2, submits, "one first attempt and one run after approval")
	assert.Greater(t, polls, 1, "the wait loop must poll, not return the first denial")
}

func TestRunExecWithApprovalWait_TimesOutAfterWindow(t *testing.T) {
	const waitTimeout = 60 * time.Millisecond
	denial := stubApprovalWaitSeams(t, 10*time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		status := "pending_approval"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("stream must not run when approval never lands")
		return nil
	}

	start := time.Now()
	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", waitTimeout, io.Discard)
	elapsed := time.Since(start)

	assert.Same(t, denial, err, "timeout returns the last pending denial for the caller's handler")
	assert.GreaterOrEqual(t, elapsed, waitTimeout, "loop must wait the full anchored window before timing out")
}

// statusError carries a status the way the API client's errors do.
type statusError struct {
	status int
}

func (e *statusError) Error() string       { return fmt.Sprintf("server said %d", e.status) }
func (e *statusError) HTTPStatusCode() int { return e.status }

func TestIsPollFailure(t *testing.T) {
	t.Parallel()
	// What a proxy error page under a JSON content type leaves the caller with.
	unparseableBody := json.Unmarshal([]byte(`<html>502 Bad Gateway</html>`), &struct{}{})
	// A body that is JSON but not the response shape: parses, answers nothing.
	driftedField := json.Unmarshal([]byte(`{"status":7}`), &struct {
		Status string `json:"status"`
	}{})

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no error is not a failure", err: nil},
		{name: "the command ran and was denied", err: &event.RemoteCommandError{ExitCode: 1}},
		{name: "a tick that spent a whole command timeout", err: &event.ClientTimeoutError{}},
		{name: "a reviewer rejected the command", err: &event.CommandRejectedError{}},
		{name: "a wrapped rejection stays an answer", err: fmt.Errorf("failed to execute command on 'srv' server: %w", &event.CommandRejectedError{})},
		// errorFromDetails reports these four as a plain error with no HTTP
		// status. Re-submitting them re-runs a command the server already
		// finished, so a status-less error must not read as a failed poll.
		{name: "a cancelled command already ran", err: errors.New("command failed with status: cancelled")},
		{name: "a stuck command already ran", err: errors.New("command failed with status: stuck")},
		{name: "a phased error status already ran", err: errors.New("command failed: [delivery] desc (status=error)")},
		{name: "an unrecognised status is still an answer", err: errors.New("unexpected command status: weird (command may still be running)")},
		{name: "a wrapped terminal status stays an answer", err: fmt.Errorf("failed to execute command on 'srv' server: %w", errors.New("command failed with status: cancelled"))},
		{name: "a dial failure never reached the server", err: &url.Error{Op: "Post", URL: "https://alpacon", Err: errors.New("connection reset")}, want: true},
		{name: "a wrapped dial failure never reached the server", err: fmt.Errorf("wrap: %w", &url.Error{Op: "Post", URL: "https://alpacon", Err: errors.New("connection reset")}), want: true},
		{name: "an unparseable body answered nothing", err: unparseableBody, want: true},
		{name: "a wrapped unparseable body answered nothing", err: fmt.Errorf("failed to execute command on 'srv' server: %w", unparseableBody), want: true},
		{name: "a drifted field type answered nothing", err: driftedField, want: true},
		{name: "a wrapped drifted field type answered nothing", err: fmt.Errorf("failed to execute command on 'srv' server: %w", driftedField), want: true},
		// readJSONResponse tags a read cut short with the status the headers gave,
		// so it arrives here as a 2xx rather than as a status-less error.
		{name: "a body cut short kept its status", err: &statusError{status: http.StatusOK}, want: true},
		{name: "throttled", err: &statusError{status: http.StatusTooManyRequests}, want: true},
		{name: "proxy error", err: &statusError{status: http.StatusBadGateway}, want: true},
		{name: "unauthorized repeats on every tick", err: &statusError{status: http.StatusUnauthorized}},
		{name: "not found repeats on every tick", err: &statusError{status: http.StatusNotFound}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isPollFailure(tt.err))
		})
	}
}

func TestRunExecWithApprovalWait_TransientPollFailureKeepsWaiting(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")

	submits := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		submits++
		if submits == 1 {
			return denial
		}
		return nil // a reviewer approved it; the post-approval run succeeds
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		if polls == 1 {
			return event.EventDetails{}, &statusError{status: http.StatusBadGateway}
		}
		status := "authorized"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("a denial-code wait polls the command detail; it never resumes a held job")
		return nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", time.Second, io.Discard)

	require.NoError(t, err)
	assert.Equal(t, 2, polls, "the failed poll must not end the wait")
}

// Reporting the last poll failure instead would exit 1 on a request still open.
func TestRunExecWithApprovalWait_TimeoutReportsThePendingDenial(t *testing.T) {
	const waitTimeout = 80 * time.Millisecond
	denial := stubApprovalWaitSeams(t, 10*time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}

	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		// Alternate so the failure count never reaches the give-up bound.
		if polls%2 == 0 {
			return event.EventDetails{}, &statusError{status: http.StatusBadGateway}
		}
		status := "pending_approval"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("stream must not run when approval never lands")
		return nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", waitTimeout, io.Discard)

	assert.Same(t, denial, err, "a timed-out wait reports the pending denial, not the last poll failure")
}

func TestRunExecWithApprovalWait_GivesUpAfterConsecutivePollFailures(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		return event.EventDetails{}, &statusError{status: http.StatusBadGateway}
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("stream must not run when approval never lands")
		return nil
	}

	var err error
	stderr := captureStderr(t, func() {
		err = RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", time.Minute, io.Discard)
	})

	// The request is still open, so giving up must exit on the pending contract
	// like the timeout does; the transport detail rides on the warning line.
	assert.Same(t, denial, err, "an unreachable server leaves the approval request open")
	assert.Contains(t, stderr, "server said 502")
	assert.Equal(t, utils.MaxConsecutivePollFailures, polls, "the bounded run of failed polls")
}

// The give-up warning carries a server-controlled string: for a non-JSON body
// the client puts the body itself into the message, so escape sequences would
// otherwise reach the terminal.
func TestRunExecWithApprovalWait_GiveUpWarningSanitizesServerText(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		return event.EventDetails{}, fmt.Errorf("\x1b[2Kbad gateway page: %w", &statusError{status: http.StatusBadGateway})
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("stream must not run when approval never lands")
		return nil
	}

	stderr := captureStderr(t, func() {
		_ = RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", time.Minute, io.Discard)
	})

	assert.NotContains(t, stderr, "\x1b[2K")
	assert.Contains(t, stderr, "bad gateway page: server said 502")
}

func TestRunExecWithApprovalWait_FatalClientErrorEndsTheWait(t *testing.T) {
	denial := stubApprovalWaitSeams(t, 10*time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		return event.EventDetails{}, &statusError{status: http.StatusUnauthorized}
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("stream must not run when approval never lands")
		return nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", time.Minute, io.Discard)

	var status *statusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, 1, polls, "a fatal 4xx must not be retried until the deadline")
}

// A rejection landing mid-wait must end the loop at once: the poll reads the
// command's own grant status, so a reviewer's rejection is visible on the very
// next tick.
// A 429 must not starve the wait: the approval may still be pending, only the
// status GET refused. Without the deadline extension the wait would time out
// before the third poll (t=80ms) ever fires.
func TestRunExecWithApprovalWait_ThrottleExtendsTheDeadline(t *testing.T) {
	const waitTimeout = 60 * time.Millisecond
	const pollInterval = 20 * time.Millisecond
	denial := stubApprovalWaitSeams(t, pollInterval, "SUDO_APPROVAL_REQUIRED")

	submits := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		submits++
		if submits == 1 {
			return denial
		}
		return nil // a reviewer approved it; the post-approval run succeeds
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		if polls < 3 {
			return event.EventDetails{}, &statusError{status: http.StatusTooManyRequests}
		}
		status := "authorized"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("a denial-code wait polls the command detail; it never resumes a held job")
		return nil
	}

	start := time.Now()
	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", waitTimeout, io.Discard)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, 2, submits, "one first attempt and one run after approval")
	assert.Equal(t, 3, polls)
	assert.GreaterOrEqual(t, elapsed, waitTimeout, "the throttle extension must carry the wait past the original deadline")
}

// A run of 429s must not end the wait through the failed-poll cap. That cap is
// for a server the CLI cannot reach; a 429 is the server answering, and the
// throttle budget plus the deadline are what bound it.
func TestRunExecWithApprovalWait_SustainedThrottleDoesNotTripTheFailureCap(t *testing.T) {
	const waitTimeout = 150 * time.Millisecond
	const pollInterval = 2 * time.Millisecond
	throttled := utils.MaxConsecutivePollFailures + 1
	denial := stubApprovalWaitSeams(t, pollInterval, "SUDO_APPROVAL_REQUIRED")

	submits := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		submits++
		if submits == 1 {
			return denial
		}
		return nil
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		if polls <= throttled {
			return event.EventDetails{}, &statusError{status: http.StatusTooManyRequests}
		}
		status := "authorized"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("a denial-code wait polls the command detail; it never resumes a held job")
		return nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", waitTimeout, io.Discard)

	require.NoError(t, err)
	assert.Equal(t, throttled+1, polls)
	assert.Equal(t, 2, submits, "one first attempt and one run after approval")
}

func TestRunExecWithApprovalWait_RejectionMidWaitEndsTheWait(t *testing.T) {
	denial := stubApprovalWaitSeams(t, 10*time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		return denial
	}
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		status := "rejected"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("stream must not run for a rejected command")
		return nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", time.Minute, io.Discard)

	var rejected *event.CommandRejectedError
	require.ErrorAs(t, err, &rejected)
}

func TestApprovalOutcomeOf(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }

	tests := []struct {
		name   string
		status *string
		want   approvalOutcome
	}{
		{"authorized approves", str("authorized"), outcomeApproved},
		{"rejected settles without a grant", str("rejected"), outcomeNotGranted},
		{"expired settles without a grant", str("expired"), outcomeNotGranted},
		{"pending_approval keeps waiting", str("pending_approval"), outcomePending},
		{"pending_mfa keeps waiting", str("pending_mfa"), outcomePending},
		{"used keeps waiting", str("used"), outcomePending},
		{"empty keeps waiting", str(""), outcomePending},
		{"absent keeps waiting", nil, outcomePending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, approvalOutcomeOf(tt.status))
		})
	}
}

// captureStderr returns everything fn writes to stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	_, stderr := testutil.CaptureOutput(t, fn)
	return stderr
}

func TestPrintPresenceStepUpLink(t *testing.T) {
	original := mfaLinkByServerName
	t.Cleanup(func() { mfaLinkByServerName = original })

	// The link is printed unconditionally: api/mfa never hands back an empty URL
	// with a nil error, so there is nothing left for this caller to filter.
	t.Run("prints the link", func(t *testing.T) {
		mfaLinkByServerName = func(*client.AlpaconClient, string) (string, error) {
			return "https://example.com/mfa", nil
		}

		stderr := captureStderr(t, func() { printPresenceStepUpLink(nil, "my-server") })

		assert.Contains(t, stderr, "https://example.com/mfa")
		assert.Contains(t, stderr, "MFA verification link")
	})

	t.Run("stays silent when the link cannot be fetched", func(t *testing.T) {
		mfaLinkByServerName = func(*client.AlpaconClient, string) (string, error) {
			return "", errors.New("failed to parse MFA URL response")
		}

		stderr := captureStderr(t, func() { printPresenceStepUpLink(nil, "my-server") })

		assert.Empty(t, stderr)
	})
}

// A status hold after approval is the other shape of "the grant did not carry
// this run", and it must reach the same warning as a repeated denial.
func TestRunExecWithApprovalWait_StatusHoldAfterApprovalWarnsWithoutReentering(t *testing.T) {
	denial := stubApprovalWaitSeams(t, time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	held := &event.PendingApprovalError{CommandID: "cmd-2"}

	submits := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		submits++
		if submits == 1 {
			return denial
		}
		return held
	}
	polls := 0
	getCommandByID = func(*client.AlpaconClient, string) (event.EventDetails, error) {
		polls++
		status := "authorized"
		return event.EventDetails{SudoGrantStatus: &status}, nil
	}

	stderr := captureStderr(t, func() {
		err := RunExecWithApprovalWait(nil, "srv", "cmd", "", "", nil, "", "", time.Minute, io.Discard)
		assert.ErrorIs(t, err, held)
	})

	assert.Equal(t, 2, submits, "the wait must not re-enter after the post-approval run")
	assert.Equal(t, 1, polls)
	assert.Contains(t, stderr, "already used")
}
