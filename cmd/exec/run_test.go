package exec

import (
	"fmt"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/stretchr/testify/assert"
)

func TestRemoteCommandOutcome(t *testing.T) {
	tests := []struct {
		name              string
		result            string
		remoteErr         *event.RemoteCommandError
		wantStdoutLine    string
		wantStderrEmpty   bool
		wantStderrPhrase  string
		wantExitCode      int
	}{
		{
			name:   "result_and_phase_propagate_exit_124",
			result: "command timed out",
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
			name:   "non_zero_exit_without_phase_skips_stderr_line",
			result: "rsync: some files vanished",
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
			name:   "empty_result_with_phase_still_emits_stderr",
			result: "",
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
			name:   "legacy_fallback_exit_1_no_phase",
			result: "boom",
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
			stdoutLine, stderrLine, exitCode := remoteCommandOutcome(tt.result, tt.remoteErr)

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
