package exec

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
)

func TestClientTimeoutLine(t *testing.T) {
	line := clientTimeoutLine()
	assert.Contains(t, line, "[client_timeout]", "stderr should carry the phase id in brackets")
	assert.Contains(t, line, event.DescribePhase("client_timeout"),
		"stderr should include the human-readable description")
	assert.True(t, strings.HasSuffix(line, "\n"), "line should end with newline")
}

func TestAsPhasedError(t *testing.T) {
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
				"stderr line should end with a newline")
		})
	}
}

func TestDetachResultLines(t *testing.T) {
	line1, line2 := detachResultLines("a1b2c3d4-1234-5678-abcd-000000000000")
	assert.Equal(t, "Job submitted: a1b2c3d4-1234-5678-abcd-000000000000", line1)
	assert.Equal(t, "Run `alpacon exec logs a1b2c3d4-1234-5678-abcd-000000000000` to check the result.", line2)
}

// stubApprovalWaitSeams swaps the loop's seams/interval (restored on cleanup) and returns a denial carrying the plugin line the loop keys on.
func stubApprovalWaitSeams(t *testing.T, interval time.Duration) *event.RemoteCommandError {
	t.Helper()
	origStepUp, origStream, origInterval := runPresenceStepUp, streamApprovedCommand, approvalWaitPollInterval
	t.Cleanup(func() {
		runPresenceStepUp, streamApprovedCommand, approvalWaitPollInterval = origStepUp, origStream, origInterval
	})
	approvalWaitPollInterval = interval
	return &event.RemoteCommandError{Output: sudoDenialLinePrefix + " (SUDO_APPROVAL_REQUIRED).", ExitCode: 1}
}

func TestRunExecWithApprovalWait_ResumePassesRemainingNotFull(t *testing.T) {
	const waitTimeout = 500 * time.Millisecond
	denial := stubApprovalWaitSeams(t, 10*time.Millisecond)

	calls := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, io.Writer) error {
		calls++
		if calls == 1 {
			return denial
		}
		// A later tick: server switched to a status-hold, so the loop resumes instead of re-running.
		return &event.PendingApprovalError{CommandID: "cmd-1"}
	}
	var gotTimeout time.Duration
	streamApprovedCommand = func(_ *client.AlpaconClient, _ string, _ io.Writer, timeout time.Duration) error {
		gotTimeout = timeout
		return nil
	}

	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", waitTimeout, io.Discard)

	assert.NoError(t, err)
	// Resume must use the remaining window, not a fresh full timeout—else the wait could reach 2× waitTimeout.
	assert.Greater(t, gotTimeout, time.Duration(0), "resume should still have time left")
	assert.Less(t, gotTimeout, waitTimeout, "resume must pass the remaining time, not the full timeout")
}

func TestRunExecWithApprovalWait_TimesOutAfterWindow(t *testing.T) {
	const waitTimeout = 60 * time.Millisecond
	denial := stubApprovalWaitSeams(t, 10*time.Millisecond)
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, io.Writer) error {
		return denial
	}
	streamApprovedCommand = func(*client.AlpaconClient, string, io.Writer, time.Duration) error {
		t.Fatal("stream must not run when approval never lands")
		return nil
	}

	start := time.Now()
	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", waitTimeout, io.Discard)
	elapsed := time.Since(start)

	assert.Same(t, denial, err, "timeout returns the last pending denial for the caller's handler")
	assert.GreaterOrEqual(t, elapsed, waitTimeout, "loop must wait the full anchored window before timing out")
}
