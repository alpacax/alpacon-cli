package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
			name:        "server name is sanitized",
			server:      "prod\x1b[2J",
			wantMessage: wantMessage,
			wantCommands: []string{
				"alpacon exec --upload --file /path/to/script.sh --file-from ./script.sh prod",
				"alpacon exec --file /path/to/script.sh --from-server prod",
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
			message, actions := useFileExecutionRefusal("cmd-1", tc.server, tc.accountFlags)
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

func TestUseFileExecutionAccountFlags(t *testing.T) {
	t.Parallel()
	assert.Empty(t, useFileExecutionAccountFlags(RemoteExecArgs{Server: "prod"}))
	assert.Equal(t,
		[]string{"-u root", "-g wheel", "--work-session ses-1"},
		useFileExecutionAccountFlags(RemoteExecArgs{Server: "prod", Username: "root", Groupname: "wheel", WorkSessionID: "ses-1"}))
}

// rejectedCommandID is a UUID because exec logs refuses any other job id.
const rejectedCommandID = "a1b2c3d4-5678-abcd-ef01-234567890abc"

// newRejectedCommandServer resolves one server, accepts the submission as
// rejectedCommandID, and answers its detail as rejected with cause, the shape
// the server reports once verification refuses the command.
func newRejectedCommandServer(cause string) *httptest.Server {
	const cmdID = rejectedCommandID
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			}
			if cause != "" {
				detail["rejection_cause"] = cause
				detail["rejection_gate"] = "approval"
			}
			_ = json.NewEncoder(w).Encode(detail)
		default:
			http.NotFound(w, r)
		}
	}))
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
				Description: "save the script to a local file, then write it to the server and run it in one step",
			},
			{
				Command:     "alpacon exec -u root --file /path/to/script.sh --from-server prod",
				Description: "or, when a copy is already saved at that path on the server, run it there",
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
		assert.Contains(t, stderr, "  alpacon exec --file /path/to/script.sh --from-server prod\n")
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
		assert.Len(t, envelope.NextActions, 2)
	})
}
