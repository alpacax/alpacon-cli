package exec

import (
	"errors"
	"fmt"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/stretchr/testify/assert"
)

func TestClientTimeoutLine(t *testing.T) {
	line := clientTimeoutLine()
	assert.Contains(t, line, "[client_timeout]", "stderr should carry the phase id in brackets")
	assert.Contains(t, line, event.DescribePhase("client_timeout"),
		"stderr should include the human-readable description")
	assert.True(t, len(line) > 0 && line[len(line)-1] == '\n', "line should end with newline")
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
				assert.Nil(t, got)
			}
		})
	}
}

func TestRemoteCommandOutcome(t *testing.T) {
	tests := []struct {
		name             string
		remoteErr        *event.RemoteCommandError
		wantStdoutLine   string
		wantStderrEmpty  bool
		wantStderrPhrase string
		wantExitCode     int
	}{
		{
			name: "result_and_phase_propagate_exit_124",
			remoteErr: &event.RemoteCommandError{
				Output:     "command timed out",
				ExitCode:   124,
				ErrorPhase: "remote_command_exceeded_timeout",
			},
			wantStdoutLine:   "command timed out",
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
			wantStdoutLine:  "rsync: some files vanished",
			wantStderrEmpty: true,
			wantExitCode:    23,
		},
		{
			name: "empty_result_with_phase_still_emits_stderr",
			remoteErr: &event.RemoteCommandError{
				Output:     "",
				ExitCode:   1,
				ErrorPhase: "agent_timeout",
			},
			wantStdoutLine:   "",
			wantStderrPhrase: "agent_timeout",
			wantExitCode:     1,
		},
		{
			name: "legacy_fallback_exit_1_no_phase",
			remoteErr: &event.RemoteCommandError{
				Output:     "boom",
				ExitCode:   1,
				ErrorPhase: "",
			},
			wantStdoutLine:  "boom",
			wantStderrEmpty: true,
			wantExitCode:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdoutLine, stderrLine, exitCode := remoteCommandOutcome(tt.remoteErr)

			assert.Equal(t, tt.wantStdoutLine, stdoutLine, "stdout line should match")
			assert.Equal(t, tt.wantExitCode, exitCode, "exit code should match")

			if tt.wantStderrEmpty {
				assert.Empty(t, stderrLine, "stderr line should be empty when no phase")
				return
			}
			assert.Contains(t, stderrLine, fmt.Sprintf("[%s]", tt.wantStderrPhrase),
				"stderr should carry the phase identifier in brackets for CI/grep")
			assert.Contains(t, stderrLine, event.DescribePhase(tt.wantStderrPhrase),
				"stderr should include the human-readable phase description")
			assert.True(t, len(stderrLine) > 0 && stderrLine[len(stderrLine)-1] == '\n',
				"stderr line should end with a newline")
		})
	}
}

func TestDetachResultLines(t *testing.T) {
	line1, line2 := detachResultLines("a1b2c3d4-1234-5678-abcd-000000000000")
	assert.Equal(t, "Job submitted: a1b2c3d4-1234-5678-abcd-000000000000", line1)
	assert.Equal(t, "Run `alpacon exec logs a1b2c3d4-1234-5678-abcd-000000000000` to check the result.", line2)
}
