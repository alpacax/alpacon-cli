package exec

import (
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/stretchr/testify/assert"
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
				assert.True(t, strings.HasSuffix(stderrLine, "\n"),
					"stderr line must end with a newline: %q", stderrLine)
			}
		})
	}
}

// The stderr line bypasses the Cli* helpers, so server-controlled fields must be
// sanitized where the line is assembled (#364). Each case injects into the one
// field an attacker controls on that branch: Status must equal a known value to
// reach the running/failed branches, so ID and ErrorPhase carry the payload
// there, while the unrecognised-status fallback renders Status itself verbatim.
func TestLogsCommandOutcomeSanitizesServerText(t *testing.T) {
	tests := []struct {
		name    string
		details event.EventDetails
		// The whole rendering, so a sanitize that ate the surrounding text fails here
		// rather than passing a check that only looks for the payload's absence.
		wantContains string
	}{
		{
			name:         "running branch strips escape and bidi in ID",
			details:      event.EventDetails{ID: "job-1\x1b[2K\u202e", Status: "running"},
			wantContains: "`alpacon exec logs job-1` again",
		},
		{
			name:         "stuck branch strips escape in unknown phase",
			details:      event.EventDetails{ID: "job-1", Status: "stuck", ErrorPhase: strPtr("boom\x1b[31m\u202e")},
			wantContains: "[boom] boom (status=stuck)",
		},
		{
			name: "failure branch strips escape in unknown phase",
			details: event.EventDetails{
				ID:         "job-1",
				Status:     "completed",
				Success:    boolPtr(false),
				ExitCode:   intPtr(2),
				ErrorPhase: strPtr("phase\x1b]0;pwn\x07\u202e"),
			},
			wantContains: "[phase] phase",
		},
		{
			name:         "unrecognised status renders sanitized",
			details:      event.EventDetails{ID: "job-1", Status: "denied\x1b[2K\u202eapproved"},
			wantContains: "unrecognised status: deniedapproved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderrLine, _ := logsCommandOutcome(tt.details)

			assert.Contains(t, stderrLine, tt.wantContains)
			assert.NotContains(t, stderrLine, "\x1b")
			assert.NotContains(t, stderrLine, "\u202e")
		})
	}
}
