package exec

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rejectionEnvelope struct {
	cliErrorEnvelope
	Server map[string]any `json:"server"`
}

func lastEnvelope(t *testing.T, stderr string) rejectionEnvelope {
	t.Helper()
	start := strings.LastIndex("\n"+stderr, "\n{")
	require.GreaterOrEqual(t, start, 0, "stderr: %s", stderr)
	var envelope rejectionEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr[start:]), &envelope), "stderr: %s", stderr)
	return envelope
}

func TestRejectedCommandReportsCauseGateAndNextStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		extra       map[string]any
		wantMessage string
		wantCode    string
		wantServer  map[string]any
		wantNext    utils.NextAction
		wantExit    int
	}{
		{
			name:        "gated cause",
			extra:       map[string]any{"rejection_cause": "risk_denied", "rejection_gate": "risk"},
			wantMessage: "was rejected: the risk assessor refused it (rejection_cause: risk_denied, rejection_gate: risk)",
			wantCode:    "risk_denied",
			wantServer:  map[string]any{"rejection_cause": "risk_denied", "rejection_gate": "risk"},
			wantNext:    utils.NextAction{Description: "The workspace risk policy refuses this command; the same command is refused the same way."},
		},
		{
			name:        "signature cause has a null gate",
			extra:       map[string]any{"rejection_cause": "unsigned_file", "rejection_gate": nil},
			wantMessage: "was rejected: the file command reached the agent without a signature (rejection_cause: unsigned_file)",
			wantCode:    "unsigned_file",
			wantServer:  map[string]any{"rejection_cause": "unsigned_file", "rejection_gate": nil},
			wantNext:    utils.NextAction{Description: "The file needs a signature; the agent runs only signed files."},
		},
		{
			name:        "null cause",
			extra:       map[string]any{"rejection_cause": nil, "rejection_gate": nil},
			wantMessage: "was rejected; the server did not record why",
			wantCode:    utils.ErrorCodeNotApproved,
			wantServer:  map[string]any{"rejection_cause": nil, "rejection_gate": nil},
			wantNext:    utils.NextAction{Description: "Check the command in the Alpacon console (web); the cause was not recorded."},
		},
		{
			name:        "agent refused the signature after delivery",
			extra:       map[string]any{"status": "completed", "success": false, "rejection_cause": "signature_invalid", "rejection_gate": nil},
			wantMessage: "was rejected: the agent could not verify the file's signature (rejection_cause: signature_invalid)",
			wantCode:    "signature_invalid",
			wantServer:  map[string]any{"rejection_cause": "signature_invalid", "rejection_gate": nil},
			wantNext:    utils.NextAction{Description: "The file's signature has to verify; the agent refuses one that does not."},
			wantExit:    utils.ExitCodeGeneralError,
		},
		{
			name:        "unknown cause falls back to its gate",
			extra:       map[string]any{"rejection_cause": "brand_new_cause", "rejection_gate": "work_session"},
			wantMessage: "was rejected: its work session refused it (rejection_cause: brand_new_cause, rejection_gate: work_session)",
			wantCode:    "brand_new_cause",
			wantServer:  map[string]any{"rejection_cause": "brand_new_cause", "rejection_gate": "work_session"},
			wantNext:    utils.NextAction{Command: "alpacon work-session current", Description: "shows the work session in use now"},
		},
	}
	surfaces := []struct {
		name, subject string
		run           func(t *testing.T, url, format string) (string, int)
	}{
		{"exec", "command " + rejectedCommandID, func(t *testing.T, url, format string) (string, int) {
			t.Helper()
			args := []string{"prod", "--", "uptime"}
			if format == utils.OutputFormatJSON {
				args = append([]string{"--output", "json"}, args...)
			}
			_, stderr, exitCode := runExecHelper(t, url, args...)
			return stderr, exitCode
		}},
		{"exec logs", "command", func(t *testing.T, url, format string) (string, int) {
			t.Helper()
			_, stderr, exitCode := runExecLogsHelper(t, url, format, rejectedCommandID)
			return stderr, exitCode
		}},
	}
	for _, surface := range surfaces {
		for _, tc := range cases {
			for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
				t.Run(surface.name+"/"+tc.name+"/"+format, func(t *testing.T) {
					t.Parallel()
					wantExit := tc.wantExit
					if wantExit == 0 {
						wantExit = utils.ExitCodeNotApproved
					}
					ts := newRejectedCommandServerWith("root", "wheel", tc.extra)
					defer ts.Close()

					stderr, exitCode := surface.run(t, ts.URL, format)

					assert.Equal(t, wantExit, exitCode, "stderr: %s", stderr)
					if format == utils.OutputFormatTable {
						assert.Contains(t, stderr, "Error: "+surface.subject+" "+tc.wantMessage+".\n")
						assert.Contains(t, stderr, "Hint: "+tc.wantNext.PlainText()+"\n")
						return
					}
					envelope := lastEnvelope(t, stderr)
					assert.False(t, envelope.OK)
					assert.Equal(t, wantExit, envelope.ExitCode)
					assert.Equal(t, tc.wantCode, envelope.ErrorCode)
					assert.Equal(t, surface.subject+" "+tc.wantMessage+".", envelope.Message)
					assert.Equal(t, map[string]any{
						"console_url":     "",
						"work_session_id": "ses-1",
						"rejection_cause": tc.wantServer["rejection_cause"],
						"rejection_gate":  tc.wantServer["rejection_gate"],
					}, envelope.Server)
					assert.Equal(t, []utils.NextAction{tc.wantNext}, envelope.NextActions)
				})
			}
		}
	}
}

func TestRejectedCommandFromOlderServerSaysTheCauseWasNotRecorded(t *testing.T) {
	t.Parallel()
	const wantMessage = "was rejected; the server did not record why."
	wantNext := utils.NextAction{Description: "Check the command in the Alpacon console (web); the cause was not recorded."}
	surfaces := []struct {
		name, subject string
		run           func(t *testing.T, url, format string) (string, int)
	}{
		{"exec", "command " + rejectedCommandID, func(t *testing.T, url, format string) (string, int) {
			t.Helper()
			args := []string{"prod", "--", "uptime"}
			if format == utils.OutputFormatJSON {
				args = append([]string{"--output", "json"}, args...)
			}
			_, stderr, exitCode := runExecHelper(t, url, args...)
			return stderr, exitCode
		}},
		{"exec logs", "command", func(t *testing.T, url, format string) (string, int) {
			t.Helper()
			_, stderr, exitCode := runExecLogsHelper(t, url, format, rejectedCommandID)
			return stderr, exitCode
		}},
	}
	for _, surface := range surfaces {
		for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
			t.Run(surface.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				ts := newRejectedCommandServerWith("root", "wheel", nil)
				defer ts.Close()

				stderr, exitCode := surface.run(t, ts.URL, format)

				assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "stderr: %s", stderr)
				assert.NotContains(t, stderr, "reviewer")
				if format == utils.OutputFormatTable {
					assert.Contains(t, stderr, "Error: "+surface.subject+" "+wantMessage+"\nHint: "+wantNext.PlainText()+"\n")
					return
				}
				envelope := lastEnvelope(t, stderr)
				assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
				assert.Equal(t, utils.ErrorCodeNotApproved, envelope.ErrorCode)
				assert.Equal(t, surface.subject+" "+wantMessage, envelope.Message)
				assert.Equal(t, map[string]any{"console_url": "", "work_session_id": "ses-1"}, envelope.Server)
				assert.Equal(t, []utils.NextAction{wantNext}, envelope.NextActions)
			})
		}
	}
}

func TestDeniedCommandReportsARefusalOnEveryExecSurface(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		extra      map[string]any
		wantCode   string
		wantReason string
		wantServer map[string]any
	}{
		{
			name: "known denial code",
			extra: map[string]any{
				"status":      "denied",
				"denial_code": "account_not_resolved",
				"result":      "The execution account could not be resolved.",
			},
			wantCode:   "account_not_resolved",
			wantReason: "The execution account could not be resolved.",
			wantServer: map[string]any{"denial_code": "account_not_resolved", "result": "The execution account could not be resolved."},
		},
		{
			name: "unknown denial code",
			extra: map[string]any{
				"status":      "denied",
				"denial_code": "new_denial_code",
				"result":      "A newer server refused this command.",
			},
			wantCode:   "new_denial_code",
			wantReason: "A newer server refused this command.",
			wantServer: map[string]any{"denial_code": "new_denial_code", "result": "A newer server refused this command."},
		},
		{
			name: "rejection cause fallback",
			extra: map[string]any{
				"status":          "denied",
				"rejection_cause": "risk_denied",
				"rejection_gate":  "risk",
			},
			wantCode:   "risk_denied",
			wantReason: "(rejection_cause: risk_denied)",
			wantServer: map[string]any{"rejection_cause": "risk_denied", "rejection_gate": "risk"},
		},
		{
			name: "file execution cause stays a terminal denial",
			extra: map[string]any{
				"status":          "denied",
				"rejection_cause": "use_file_execution",
				"rejection_gate":  "approval",
				"result":          "The command did not run.",
			},
			wantCode:   "use_file_execution",
			wantReason: "The command did not run.",
			wantServer: map[string]any{"rejection_cause": "use_file_execution", "rejection_gate": "approval", "result": "The command did not run."},
		},
		{
			name: "signature cause stays a terminal denial",
			extra: map[string]any{
				"status":          "denied",
				"rejection_cause": "signature_invalid",
				"rejection_gate":  nil,
				"result":          "The command did not run.",
			},
			wantCode:   "signature_invalid",
			wantReason: "The command did not run.",
			wantServer: map[string]any{"rejection_cause": "signature_invalid", "rejection_gate": nil, "result": "The command did not run."},
		},
		{
			name:       "denial code named like a WorkSession gate code",
			extra:      map[string]any{"status": "denied", "denial_code": "work_session_expired", "result": ""},
			wantCode:   "work_session_expired",
			wantReason: "(denial_code: work_session_expired)",
			wantServer: map[string]any{"denial_code": "work_session_expired"},
		},
		{
			name:       "missing reason",
			extra:      map[string]any{"status": "denied", "result": ""},
			wantCode:   utils.ErrorCodeNotApproved,
			wantReason: "was denied; the command did not run",
		},
	}
	surfaces := []struct {
		name string
		run  func(t *testing.T, url, format string) (string, string, int)
	}{
		{"exec", func(t *testing.T, url, format string) (string, string, int) {
			t.Helper()
			args := []string{"prod", "--", "uptime"}
			if format == utils.OutputFormatJSON {
				args = append([]string{"--output", "json"}, args...)
			}
			return runExecHelper(t, url, args...)
		}},
		{"exec logs", func(t *testing.T, url, format string) (string, string, int) {
			t.Helper()
			return runExecLogsHelper(t, url, format, rejectedCommandID)
		}},
	}
	for _, surface := range surfaces {
		for _, tc := range tests {
			for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
				t.Run(surface.name+"/"+tc.name+"/"+format, func(t *testing.T) {
					t.Parallel()
					ts := newRejectedCommandServerWith("root", "wheel", tc.extra)
					defer ts.Close()

					stdout, stderr, exitCode := surface.run(t, ts.URL, format)

					assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "stderr: %s", stderr)
					assert.Empty(t, strings.TrimSuffix(stdout, "PASS\n"))
					assert.Contains(t, stderr, "denied")
					assert.Contains(t, stderr, "command did not run")
					assert.NotContains(t, stderr, "may still be running")
					assert.Contains(t, stderr, tc.wantReason)
					if format == utils.OutputFormatTable {
						return
					}
					envelope := lastEnvelope(t, stderr)
					assert.False(t, envelope.OK)
					assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
					assert.Equal(t, tc.wantCode, envelope.ErrorCode)
					for key, value := range tc.wantServer {
						assert.Equal(t, value, envelope.Server[key], "server field %s", key)
					}
				})
			}
		}
	}
}

func TestDeniedCommandSanitizesTheDisplayedResult(t *testing.T) {
	t.Parallel()
	ts := newRejectedCommandServerWith("root", "wheel", map[string]any{
		"status":      "denied",
		"denial_code": "account_not_resolved",
		"result":      "account \x1b[31mroot\x1b[0m\u202e",
	})
	defer ts.Close()

	_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, rejectedCommandID)

	assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stderr, "account root")
	assert.NotContains(t, stderr, "\x1b")
	assert.NotContains(t, stderr, "\u202e")
}
