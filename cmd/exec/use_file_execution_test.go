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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUseFileExecutionRefusal(t *testing.T) {
	t.Parallel()
	const wantMessage = "the server refused command cmd-1 on 'prod': its command line carries a script body, which runs only as a verified file (use_file_execution)"
	cases := []struct {
		name         string
		server       string
		accountFlags []string
		withEnv      bool
		wantMessage  string
		wantCommands []string
	}{
		{
			name:        "no account flags",
			server:      "prod",
			wantMessage: wantMessage,
			wantCommands: []string{
				"alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh prod",
				"alpacon exec --file /path/to/script.sh --from-server prod",
			},
		},
		{
			name:         "account flags are repeated",
			server:       "prod",
			accountFlags: []string{"-u root", "-g wheel", "--work-session ses-1"},
			wantMessage:  wantMessage,
			wantCommands: []string{
				"alpacon exec -u root -g wheel --work-session ses-1 --upload --file /path/to/script.sh --file-from ./script.sh prod",
				"alpacon exec -u root -g wheel --work-session ses-1 --file /path/to/script.sh --from-server prod",
			},
		},
		{
			name:        "env values are warned about",
			server:      "prod",
			withEnv:     true,
			wantMessage: wantMessage + ". --env values cannot ride the file lane, so the script must read them on the host",
			wantCommands: []string{
				"alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh prod",
				"alpacon exec --file /path/to/script.sh --from-server prod",
			},
		},
		{
			name:        "server name is sanitized",
			server:      "prod\x1b[2J",
			wantMessage: wantMessage,
			wantCommands: []string{
				"alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh prod",
				"alpacon exec --file /path/to/script.sh --from-server prod",
			},
		},
		{
			name:        "server name with shell syntax is quoted",
			server:      "prod;id",
			wantMessage: "the server refused command cmd-1 on 'prod;id': its command line carries a script body, which runs only as a verified file (use_file_execution)",
			wantCommands: []string{
				"alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh 'prod;id'",
				"alpacon exec --file /path/to/script.sh --from-server 'prod;id'",
			},
		},
		{
			name:        "missing server gets a placeholder",
			server:      "",
			wantMessage: "the server refused command cmd-1 on 'SERVER': its command line carries a script body, which runs only as a verified file (use_file_execution)",
			wantCommands: []string{
				"alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh SERVER",
				"alpacon exec --file /path/to/script.sh --from-server SERVER",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			message, actions := useFileExecutionRefusal("cmd-1", tc.server, tc.accountFlags, tc.withEnv, "")
			assert.Equal(t, tc.wantMessage, message)
			commands := make([]string, 0, len(actions))
			for _, a := range actions {
				commands = append(commands, a.Command)
				assert.NotEmpty(t, a.Description)
			}
			assert.Equal(t, tc.wantCommands, commands)
		})
	}
}

func TestRunAsFlags(t *testing.T) {
	t.Parallel()
	assert.Empty(t, runAsFlags(RemoteExecArgs{Server: "prod"}))
	assert.Equal(t,
		[]string{"-u root", "-g wheel", "--work-session ses-1"},
		runAsFlags(RemoteExecArgs{Server: "prod", Username: "root", Groupname: "wheel", WorkSessionID: "ses-1"}))
	assert.Equal(t,
		[]string{"-u 'a b'", "-g '$(id)'", "--work-session 'ses;1'"},
		runAsFlags(RemoteExecArgs{Server: "prod", Username: "a b", Groupname: "$(id)", WorkSessionID: "ses;1"}))
}

// rejectedCommandID is a UUID because exec logs refuses any other job id.
const rejectedCommandID = "a1b2c3d4-5678-abcd-ef01-234567890abc"

// newRejectedCommandServer resolves one server, accepts the submission as
// rejectedCommandID, and answers its detail as rejected with cause, the shape
// the server reports once verification refuses the command.
func newRejectedCommandServer(cause string) *httptest.Server {
	return newRejectedCommandServerAs(cause, "root", "wheel")
}

// newRejectedCommandServerAs is newRejectedCommandServer with the detail's
// account; an empty username or groupname leaves the field out.
func newRejectedCommandServerAs(cause, username, groupname string) *httptest.Server {
	ts, _ := newRejectedCommandSequenceServer(cause, username, groupname, false, "")
	return ts
}

func newRejectedCommandSequenceServer(cause, username, groupname string, wait bool, grantStatus string) (*httptest.Server, *atomic.Int64) {
	const cmdID = rejectedCommandID
	var reads atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"` + cmdID + `"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+cmdID+"/":
			detail := map[string]any{
				"id":                  cmdID,
				"status":              "rejected",
				"verification_status": "rejected",
				"result":              "Save the script to a file and run it as a verified file.",
				"server":              map[string]any{"id": "srv-1", "name": "prod"},
				"work_session":        map[string]any{"id": "ses-1"},
			}
			read := reads.Add(1)
			if wait {
				detail["sudo_grant_status"] = grantStatus
				if read <= 2 {
					detail["status"] = "completed"
					detail["success"] = false
					detail["exit_code"] = 1
					detail["result"] = denialLine("SUDO_APPROVAL_REQUIRED")
					detail["sudo_grant_status"] = "pending_approval"
				}
			}
			if username != "" {
				detail["username"] = username
			}
			if groupname != "" {
				detail["groupname"] = groupname
			}
			if cause != "" && (!wait || read > 2) {
				detail["rejection_cause"] = cause
				detail["rejection_gate"] = "approval"
			}
			_ = json.NewEncoder(w).Encode(detail)
		default:
			http.NotFound(w, r)
		}
	}))
	return ts, &reads
}

// cliErrorEnvelope is the --output json error envelope on stderr.
type cliErrorEnvelope struct {
	OK          bool               `json:"ok"`
	ExitCode    int                `json:"exit_code"`
	ErrorCode   string             `json:"error_code"`
	Message     string             `json:"message"`
	NextActions []utils.NextAction `json:"next_actions"`
}

// TestExecUseFileExecutionRefusalPrintsGuidance drives the refusal through the
// real exec command: the run exits 6, names the code for a script reading
// stderr, and points at --file on the same server and account.
func TestExecUseFileExecutionRefusalPrintsGuidance(t *testing.T) {
	t.Parallel()

	t.Run("table", func(t *testing.T) {
		t.Parallel()
		ts := newRejectedCommandServer("use_file_execution")
		defer ts.Close()

		stdout, stderr, exitCode := runExecHelper(t, ts.URL, "root@prod", "--", "bash", "-c", "echo one\necho two")
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "stderr: %s", stderr)
		// Table mode prints the command's result as it arrives, and for this
		// refusal that is the server's own wording of it.
		assert.Contains(t, stdout, "Save the script to a file and run it as a verified file.")
		assert.Contains(t, stderr, "(use_file_execution)")
		assert.Contains(t, stderr, "Hint:")
		assert.Contains(t, stderr, "  alpacon exec -u root --upload --file /path/to/script.sh --file-from ./script.sh prod\n")
		assert.Contains(t, stderr, "  alpacon exec -u root --file /path/to/script.sh --from-server prod\n")
		assert.NotContains(t, stderr, "rejected by a reviewer")
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		ts := newRejectedCommandServer("use_file_execution")
		defer ts.Close()

		stdout, stderr, exitCode := runExecHelper(t, ts.URL, "--output", "json", "-u", "root", "prod", "--", "bash", "-c", "echo one\necho two")
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
		// The command never ran: the server's wording of the refusal stays off
		// stdout. The helper process is a go test binary and appends PASS.
		assert.Empty(t, strings.TrimSuffix(stdout, "PASS\n"))
		// The fake server has no event session, so a polling-fallback warning
		// precedes the envelope.
		start := strings.Index(stderr, "{")
		require.GreaterOrEqual(t, start, 0, "stderr: %s", stderr)
		var envelope cliErrorEnvelope
		require.NoError(t, json.Unmarshal([]byte(stderr[start:]), &envelope), "stderr: %s", stderr)
		assert.False(t, envelope.OK)
		assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
		assert.Equal(t, "use_file_execution", envelope.ErrorCode)
		assert.Contains(t, envelope.Message, "verified file")
		assert.Equal(t, []utils.NextAction{
			{
				Command:     "alpacon exec -u root --upload --file /path/to/script.sh --file-from ./script.sh prod",
				Description: "save the script to a local file, then write it to the server and run it in one step, adding --overwrite to replace a file already at that path and --interpreter ABSOLUTE_PATH for a script /bin/bash does not run",
			},
			{
				Command:     "alpacon exec -u root --file /path/to/script.sh --from-server prod",
				Description: "when a copy is already saved at that path on the server, run it there, adding --interpreter ABSOLUTE_PATH for a script /bin/bash does not run",
			},
		}, envelope.NextActions)
	})
}

// TestExecReviewerRejectionKeepsItsSentence pins that a rejection with no cause
// still reads as a reviewer's refusal and gets no --file guidance.
func TestExecReviewerRejectionKeepsItsSentence(t *testing.T) {
	t.Parallel()
	ts := newRejectedCommandServer("")
	defer ts.Close()

	_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "uptime")
	assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
	assert.Contains(t, stderr, "rejected by a reviewer")
	assert.NotContains(t, stderr, "--file")
}

// Expiry and human rejection share exit 6 but carry distinct messages and JSON codes.
func TestApprovalRejectionCauseMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		format      string
		wait        bool
		grantStatus string
		run         func(t *testing.T, serverURL, format string) (string, int)
	}{
		{
			name:   "exec table",
			format: utils.OutputFormatTable,
			run: func(t *testing.T, serverURL, _ string) (string, int) {
				t.Helper()
				_, stderr, exitCode := runExecHelper(t, serverURL, "prod", "--", "uptime")
				return stderr, exitCode
			},
		},
		{
			name:   "exec json",
			format: utils.OutputFormatJSON,
			run: func(t *testing.T, serverURL, _ string) (string, int) {
				t.Helper()
				_, stderr, exitCode := runExecHelper(t, serverURL, "--output", "json", "prod", "--", "uptime")
				return stderr, exitCode
			},
		},
		{
			name:        "exec wait table",
			grantStatus: "rejected",
			wait:        true,
			format:      utils.OutputFormatTable,
			run: func(t *testing.T, serverURL, _ string) (string, int) {
				t.Helper()
				_, stderr, exitCode := runExecWaitHelper(t, serverURL, "--wait", "--wait-approval", "300ms", "prod", "--", "uptime")
				return stderr, exitCode
			},
		},
		{
			name:        "exec wait json",
			grantStatus: "pending_approval",
			wait:        true,
			format:      utils.OutputFormatJSON,
			run: func(t *testing.T, serverURL, _ string) (string, int) {
				t.Helper()
				_, stderr, exitCode := runExecWaitHelper(t, serverURL, "--output", "json", "--wait", "--wait-approval", "300ms", "prod", "--", "uptime")
				return stderr, exitCode
			},
		},
		{
			name:   "exec logs table",
			format: utils.OutputFormatTable,
			run: func(t *testing.T, serverURL, format string) (string, int) {
				t.Helper()
				_, stderr, exitCode := runExecLogsHelper(t, serverURL, format, rejectedCommandID)
				return stderr, exitCode
			},
		},
		{
			name:   "exec logs json",
			format: utils.OutputFormatJSON,
			run: func(t *testing.T, serverURL, format string) (string, int) {
				t.Helper()
				_, stderr, exitCode := runExecLogsHelper(t, serverURL, format, rejectedCommandID)
				return stderr, exitCode
			},
		},
	}
	causes := []struct {
		name, cause, code, message string
	}{
		{name: "expired", cause: event.RejectionCauseApprovalExpired, code: event.RejectionCauseApprovalExpired, message: "was not approved before the approval request expired"},
		{name: "reviewer", cause: event.RejectionCauseApprovalRejected, code: event.RejectionCauseApprovalRejected, message: "was rejected by a reviewer"},
		{name: "other", cause: "risk_denied", code: "risk_denied", message: "was refused (risk_denied)"},
		{name: "older server", code: "not_approved", message: "was rejected by a reviewer"},
	}
	for _, cause := range causes {
		t.Run(cause.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					ts, reads := newRejectedCommandSequenceServer(cause.cause, "root", "wheel", tc.wait, tc.grantStatus)
					defer ts.Close()

					stderr, exitCode := tc.run(t, ts.URL, tc.format)
					assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "stderr: %s", stderr)
					assert.Contains(t, stderr, cause.message)
					if tc.wait {
						assert.EqualValues(t, 3, reads.Load(), "submission read, pending poll, then terminal poll")
					}
					assert.NotContains(t, stderr, "--file")
					if tc.format != utils.OutputFormatJSON {
						return
					}
					// exec may print a polling-fallback warning ahead of the envelope,
					// so the envelope starts at the last line that opens with a brace.
					start := strings.LastIndex("\n"+stderr, "\n{")
					require.GreaterOrEqual(t, start, 0, "stderr: %s", stderr)
					var envelope cliErrorEnvelope
					require.NoError(t, json.Unmarshal([]byte(stderr[start:]), &envelope), "stderr: %s", stderr)
					assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
					assert.Equal(t, cause.code, envelope.ErrorCode)
				})
			}
		})
	}
}

func TestLogsAccountFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		details   event.EventDetails
		wantFlags []string
		wantNote  string
	}{
		{
			name:      "username without groupname and no work session",
			details:   event.EventDetails{Username: "root"},
			wantFlags: []string{"-u root"},
		},
		{
			name:      "username without groupname with a work session",
			details:   event.EventDetails{Username: "root", WorkSession: &event.CommandWorkSession{ID: "ses-1"}},
			wantFlags: []string{"-u root", "--work-session ses-1"},
		},
		{
			name:      "username and groupname and no work session",
			details:   event.EventDetails{Username: "root", Groupname: "wheel"},
			wantFlags: []string{"-u root", "-g wheel"},
		},
		{
			name:      "groupname without username and no work session",
			details:   event.EventDetails{Groupname: "wheel"},
			wantFlags: []string{"-u ACCOUNT", "-g wheel"},
			wantNote:  "the command detail does not name the account the command ran as, so replace ACCOUNT with that account: without -u the command runs as the server's default account",
		},
		{
			name:      "no account and no work session",
			details:   event.EventDetails{},
			wantFlags: []string{"-u ACCOUNT", "-g GROUP"},
			wantNote:  "the command detail does not name the account the command ran as, so replace ACCOUNT with that account and GROUP with its group, or drop -g if it had none: without -u the command runs as the server's default account",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			flags, note := logsAccountFlags(tc.details)
			assert.Equal(t, tc.wantFlags, flags)
			assert.Equal(t, tc.wantNote, note)
		})
	}
}

// TestExecLogsUseFileExecutionRefusal covers the detach path: exec logs reads
// the refusal from the command detail and gives the same guidance.
func TestExecLogsUseFileExecutionRefusal(t *testing.T) {
	t.Parallel()

	t.Run("table", func(t *testing.T) {
		t.Parallel()
		ts := newRejectedCommandServer("use_file_execution")
		defer ts.Close()

		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, rejectedCommandID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "stderr: %s", stderr)
		assert.Contains(t, stderr, "(use_file_execution)")
		assert.Contains(t, stderr, "  alpacon exec -u root -g wheel --work-session ses-1 --upload --file /path/to/script.sh --file-from ./script.sh prod\n")
		assert.Contains(t, stderr, "  alpacon exec -u root -g wheel --work-session ses-1 --file /path/to/script.sh --from-server prod\n")
		assert.NotContains(t, stderr, "rejected by a reviewer")
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		ts := newRejectedCommandServer("use_file_execution")
		defer ts.Close()

		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, rejectedCommandID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
		var envelope cliErrorEnvelope
		require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
		assert.Equal(t, "use_file_execution", envelope.ErrorCode)
		assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
		commands := make([]string, 0, len(envelope.NextActions))
		for _, a := range envelope.NextActions {
			commands = append(commands, a.Command)
		}
		assert.Equal(t, []string{
			"alpacon exec -u root -g wheel --work-session ses-1 --upload --file /path/to/script.sh --file-from ./script.sh prod",
			"alpacon exec -u root -g wheel --work-session ses-1 --file /path/to/script.sh --from-server prod",
		}, commands)
	})
}

// TestExecLogsUseFileExecutionRefusalWithoutAccount pins that a detail naming no
// account never yields a runnable command: dropping -u would run the pasted
// line as the server's default account, so both commands carry placeholders
// and the message says to fill them in.
func TestExecLogsUseFileExecutionRefusalWithoutAccount(t *testing.T) {
	t.Parallel()

	t.Run("table", func(t *testing.T) {
		t.Parallel()
		ts := newRejectedCommandServerAs("use_file_execution", "", "")
		defer ts.Close()

		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, rejectedCommandID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode, "stderr: %s", stderr)
		assert.Contains(t, stderr, "replace ACCOUNT with that account")
		assert.Contains(t, stderr, "  alpacon exec -u ACCOUNT -g GROUP --work-session ses-1 --upload --file /path/to/script.sh --file-from ./script.sh prod\n")
		assert.Contains(t, stderr, "  alpacon exec -u ACCOUNT -g GROUP --work-session ses-1 --file /path/to/script.sh --from-server prod\n")
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		ts := newRejectedCommandServerAs("use_file_execution", "", "")
		defer ts.Close()

		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, rejectedCommandID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
		var envelope cliErrorEnvelope
		require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
		assert.Contains(t, envelope.Message, "replace ACCOUNT with that account")
		commands := make([]string, 0, len(envelope.NextActions))
		for _, a := range envelope.NextActions {
			commands = append(commands, a.Command)
		}
		assert.Equal(t, []string{
			"alpacon exec -u ACCOUNT -g GROUP --work-session ses-1 --upload --file /path/to/script.sh --file-from ./script.sh prod",
			"alpacon exec -u ACCOUNT -g GROUP --work-session ses-1 --file /path/to/script.sh --from-server prod",
		}, commands)
	})

	t.Run("group named", func(t *testing.T) {
		t.Parallel()
		ts := newRejectedCommandServerAs("use_file_execution", "", "wheel")
		defer ts.Close()

		_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, rejectedCommandID)
		assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
		var envelope cliErrorEnvelope
		require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), "stderr: %s", stderr)
		assert.NotContains(t, envelope.Message, "GROUP")
		require.Len(t, envelope.NextActions, 2)
		assert.Equal(t,
			"alpacon exec -u ACCOUNT -g wheel --work-session ses-1 --file /path/to/script.sh --from-server prod",
			envelope.NextActions[1].Command)
	})
}

// TestReRunHintQuotesRunAsFlags pins that the pending-approval hint quotes the
// account flags the same way the use_file_execution guidance does.
func TestReRunHintQuotesRunAsFlags(t *testing.T) {
	t.Parallel()
	hint := reRunHint(RemoteExecArgs{Server: "prod", Command: "uptime", Username: "root", Groupname: "a b", WorkSessionID: "ses;1"})
	assert.Equal(t, "alpacon exec -u root -g 'a b' --work-session 'ses;1' prod -- uptime", hint.Command)
}

// TestReRunHintQuotesServer pins that the pending-approval hint quotes the
// server token on both lanes, as useFileExecutionRefusal does.
func TestReRunHintQuotesServer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		parsed RemoteExecArgs
		want   string
	}{
		{
			name:   "command lane",
			parsed: RemoteExecArgs{Server: "web;01", Command: "uptime"},
			want:   "alpacon exec 'web;01' -- uptime",
		},
		{
			name:   "file lane",
			parsed: RemoteExecArgs{Server: "web 01", File: &FileExecArgs{Path: "/opt/run.sh", FromServer: true}},
			want:   "alpacon exec --file /opt/run.sh --from-server 'web 01'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, reRunHint(tc.parsed).Command)
		})
	}
}

// TestReportCodedRefusalInvalidIDExitCode pins that an ErrInvalidID refusal
// under --output json exits 2 with error_code usage_error, so the code and
// the exit code agree, while table mode keeps the caller's exit code.
func TestReportCodedRefusalInvalidIDExitCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		format   string
		wantExit int
	}{
		{name: "json", format: utils.OutputFormatJSON, wantExit: utils.ExitCodeUsageError},
		{name: "table", format: utils.OutputFormatTable, wantExit: utils.ExitCodeNotApproved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			helper := osexec.Command(os.Args[0], "-test.run=^TestReportCodedRefusalInvalidIDHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_CODED_REFUSAL_INVALID_ID_HELPER="+tc.format)
			var stderr bytes.Buffer
			helper.Stderr = &stderr

			err := helper.Run()
			var exitErr *osexec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tc.wantExit, exitErr.ExitCode())
			if tc.format == utils.OutputFormatTable {
				assert.Contains(t, stderr.String(), "work session ID is unusable.")
				return
			}
			var envelope struct {
				ExitCode  int    `json:"exit_code"`
				ErrorCode string `json:"error_code"`
				Message   string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
			assert.Equal(t, utils.ExitCodeUsageError, envelope.ExitCode)
			assert.Equal(t, utils.ErrorCodeUsage, envelope.ErrorCode)
			assert.Equal(t, "work session ID is unusable.", envelope.Message)
		})
	}
}

func TestReportCodedRefusalInvalidIDHelperProcess(t *testing.T) {
	format := os.Getenv("GO_WANT_CODED_REFUSAL_INVALID_ID_HELPER")
	if format == "" {
		return
	}
	utils.OutputFormat = format
	err := fmt.Errorf("work session ID: %w", utils.ErrInvalidID)
	reportCodedRefusalWithExit(utils.ExitCodeNotApproved, "command", err, "work session ID is unusable", "", nil)
}

func TestReportCodedRefusalOfAUsageErrorExitsTwoOnlyInPlaceOfOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		code     string
		wantExit int
	}{
		{name: "exit code 1 becomes 2", code: "1", wantExit: utils.ExitCodeUsageError},
		{name: "a chosen exit code other than 1 is kept", code: "6", wantExit: utils.ExitCodeNotApproved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			helper := osexec.Command(os.Args[0], "-test.run=^TestReportCodedRefusalUsageErrorHelperProcess$")
			helper.Env = append(os.Environ(), "GO_WANT_CODED_REFUSAL_USAGE_HELPER="+tc.code)
			var stderr bytes.Buffer
			helper.Stderr = &stderr

			err := helper.Run()

			var exitErr *osexec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tc.wantExit, exitErr.ExitCode())
			assert.Contains(t, stderr.String(), "the flag is unusable.")
		})
	}
}

func TestReportCodedRefusalUsageErrorHelperProcess(t *testing.T) {
	code := os.Getenv("GO_WANT_CODED_REFUSAL_USAGE_HELPER")
	if code == "" {
		return
	}
	utils.OutputFormat = utils.OutputFormatTable
	err := utils.MarkUsageError(errors.New("bad flag"))
	if code == "1" {
		reportCodedRefusal("command", err, "the flag is unusable", "")
	}
	reportCodedRefusalWithExit(utils.ExitCodeNotApproved, "command", err, "the flag is unusable", "", nil)
}
