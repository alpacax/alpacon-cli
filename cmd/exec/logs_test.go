package exec

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	t.Parallel()
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
	t.Parallel()
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

// The stderr line bypasses the Cli* helpers, so each case injects into the one
// field the server controls on that branch: Status must equal a known value to
// reach the running and failed branches, so ID and ErrorPhase carry the payload
// there, while the unrecognised-status fallback renders Status itself (#364).
func TestLogsCommandOutcomeSanitizesServerText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		details    event.EventDetails
		wantStderr string
	}{
		{
			name:       "running branch strips escape and bidi in ID",
			details:    event.EventDetails{ID: "job-1\x1b[2K\u202e", Status: "running"},
			wantStderr: "command is still running (status: running).\nRun `alpacon exec logs job-1` again to check later.\n",
		},
		{
			name:       "stuck branch strips escape in unknown phase",
			details:    event.EventDetails{ID: "job-1", Status: "stuck", ErrorPhase: strPtr("boom\x1b[31m\u202e")},
			wantStderr: utils.Red("Error") + ": [boom] boom (status=stuck)\n",
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
			wantStderr: utils.Red("Error") + ": [phase] phase\n",
		},
		{
			name:       "unrecognised status renders sanitized",
			details:    event.EventDetails{ID: "job-1", Status: "denied\x1b[2K\u202eapproved"},
			wantStderr: utils.Red("Error") + ": command ended with unrecognised status: deniedapproved\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderrLine, _ := logsCommandOutcome(tt.details)

			assert.Equal(t, tt.wantStderr, stderrLine)
		})
	}
}

// TestExecLogsChunkFetchFailureExitsNonZero pins the failure a discarded chunk
// error hides. Output lives in chunks under the streaming contract, so a 500
// there leaves Result empty and nothing to print, and exit 0 tells the caller
// the command finished having produced no output.
func TestExecLogsChunkFetchFailureExitsNonZero(t *testing.T) {
	t.Parallel()
	const jobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/events/commands/" + jobID + "/chunks/":
			w.WriteHeader(http.StatusInternalServerError)
		case "/api/events/commands/" + jobID + "/":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": jobID, "status": "completed", "success": true, "exit_code": 0, "result": "",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	stdout, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, jobID)
	assert.Equal(t, 1, exitCode, "unreadable output is a failure, not a command that printed nothing")
	assert.Contains(t, stderr, "failed to fetch command output")
	assert.Empty(t, stdout)
}

func TestResolveCommandOutput(t *testing.T) {
	t.Parallel()
	fetchErr := errors.New("unexpected response from server (HTTP 500)")
	tests := []struct {
		name    string
		result  string
		chunked string
		err     error
		want    string
		wantErr error
	}{
		{name: "chunks win over the legacy field", result: "legacy", chunked: "chunked", want: "chunked"},
		{name: "no chunks falls back to the legacy field", result: "legacy", want: "legacy"},
		{name: "a command that printed nothing stays empty", want: ""},
		{name: "fetch failure falls back to a legacy result", result: "legacy", err: fetchErr, want: "legacy"},
		{name: "fetch failure with nothing to fall back on", err: fetchErr, wantErr: fetchErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveCommandOutput(tt.result, tt.chunked, tt.err)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
