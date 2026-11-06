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
					assert.Equal(t, tc.wantServer, envelope.Server)
					assert.Equal(t, []utils.NextAction{tc.wantNext}, envelope.NextActions)
				})
			}
		}
	}
}

func TestRejectedCommandFromOlderServerSaysTheCauseWasNotRecorded(t *testing.T) {
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
				assert.Nil(t, envelope.Server)
				assert.Equal(t, []utils.NextAction{wantNext}, envelope.NextActions)
			})
		}
	}
}
