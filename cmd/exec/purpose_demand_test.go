package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const execPurposeHelperMarker = "--exec-purpose-helper--"

// TestExecPurposeHelperProcess runs `exec purpose` in the child process, the way
// TestExecLogsHelperProcess does for `exec logs`.
func TestExecPurposeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_PURPOSE_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, execPurposeHelperMarker)
	if !ok {
		fmt.Fprintln(os.Stderr, "missing "+execPurposeHelperMarker+" marker")
		os.Exit(2)
	}
	purposeCmd.Run(purposeCmd, args)
}

func runExecPurposeHelper(t *testing.T, workspaceURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperProcess(t, workspaceURL,
		"TestExecPurposeHelperProcess", execPurposeHelperMarker,
		[]string{"GO_WANT_EXEC_PURPOSE_HELPER=1"}, args)
}

// runExecPurposeOK is runExecPurposeHelper for the success path, which
// runHelperProcess cannot serve: it requires a non-zero exit.
func runExecPurposeOK(t *testing.T, home string, args ...string) string {
	t.Helper()
	helper := osexec.Command(os.Args[0],
		append([]string{"-test.run=^TestExecPurposeHelperProcess$", "--", execPurposeHelperMarker}, args...)...)
	helper.Env = append(os.Environ(),
		"ALPACON_WORK_SESSION=", "HOME="+home, "GO_WANT_EXEC_PURPOSE_HELPER=1")
	var outBuf, errBuf bytes.Buffer
	helper.Stdout = &outBuf
	helper.Stderr = &errBuf
	require.NoError(t, helper.Run(), "stderr: %s", errBuf.String())
	return errBuf.String()
}

// purposeRequiredSignal is the machine-readable half of the purpose-demand
// contract these tests assert on. Its flags are what separate it from a pending
// approval: nothing is waiting on a human, and the answer is the caller's.
type purposeRequiredSignal struct {
	OK                    bool               `json:"ok"`
	Status                string             `json:"status"`
	ExitCode              int                `json:"exit_code"`
	CommandID             string             `json:"command_id"`
	RequiresHumanApproval bool               `json:"requires_human_approval"`
	AnswerableByCaller    bool               `json:"answerable_by_caller"`
	Guidance              string             `json:"guidance"`
	NextActions           []utils.NextAction `json:"next_actions"`
}

// newAwaitingPurposeServer parks the submitted command at "awaiting_purpose",
// the state the gate leaves it in while it asks what the command is for. The
// submitted body is captured so a test can assert what the CLI declared.
func newAwaitingPurposeServer(submitted *atomic.Value) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if submitted != nil {
				submitted.Store(body)
			}
			_, _ = fmt.Fprintf(w, `[{"id":"cmd-1"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmd-1",
				"status": "awaiting_purpose",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestExecAwaitingPurposeExits7WithJSONSignal drives a parked command through
// the real exec command. Exit 7 rather than 4 is the point: a caller that reads
// this as a pending approval waits for a human who was never asked, and the
// demand expires while it waits.
func TestExecAwaitingPurposeExits7WithJSONSignal(t *testing.T) {
	ts := newAwaitingPurposeServer(nil)
	defer ts.Close()

	stdout, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "bash", "/tmp/rotate.sh")
	assert.Equal(t, utils.ExitCodePurposeRequired, exitCode, "a purpose demand must exit 7, not the approval code")

	var got purposeRequiredSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.False(t, got.OK)
	assert.Equal(t, utils.PurposeRequiredStatus, got.Status)
	assert.Equal(t, utils.ExitCodePurposeRequired, got.ExitCode)
	assert.Equal(t, "cmd-1", got.CommandID)

	// The two flags a consumer branches on. Inverted from the approval envelope
	// because no approval request exists while the demand is open.
	assert.False(t, got.RequiresHumanApproval, "nobody has been asked to approve anything yet")
	assert.True(t, got.AnswerableByCaller, "the answer is this caller's to give")

	require.NotEmpty(t, got.NextActions)
	assert.Contains(t, got.NextActions[0].Command, "alpacon exec purpose cmd-1",
		"the leading action must be answering, not waiting")

	// Guidance travels in the response, not only in help text: a consumer reads
	// the answer to its own call more reliably than documentation.
	assert.Contains(t, got.Guidance, "local to this host")
	assert.Contains(t, got.Guidance, "cannot lower")
}

// TestExecDeclaresPurposeDemandSupport pins the field the gate arms on. Without
// it the server feature is unreachable from the CLI, however complete the rest
// of this file is.
func TestExecDeclaresPurposeDemandSupport(t *testing.T) {
	var submitted atomic.Value
	ts := newAwaitingPurposeServer(&submitted)
	defer ts.Close()

	_, _, exitCode := runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "bash", "/tmp/rotate.sh")
	require.Equal(t, utils.ExitCodePurposeRequired, exitCode)

	body, ok := submitted.Load().(map[string]any)
	require.True(t, ok, "no command submission captured")
	assert.Equal(t, true, body["purpose_demand_supported"], "the gate does not arm without the declaration")
	// Not stated up front here, so the field must be absent rather than blank:
	// an empty purpose is not the same as no purpose to the arming check.
	_, hasPurpose := body["purpose"]
	assert.False(t, hasPurpose, "an unstated purpose must not be sent as an empty string")
}

// TestExecSendsTheStatedPurpose covers the path the ADR actually intends: with a
// purpose in hand the assessor judges on the first pass and no demand is issued.
func TestExecSendsTheStatedPurpose(t *testing.T) {
	var submitted atomic.Value
	ts := newAwaitingPurposeServer(&submitted)
	defer ts.Close()

	_, _, exitCode := runExecHelper(t, ts.URL, "--output", "json",
		"--purpose", "The host clock is 40s ahead, so the renewed cert reads as not-yet-valid.",
		"prod", "--", "systemctl", "restart", "chronyd")
	require.Equal(t, utils.ExitCodePurposeRequired, exitCode)

	body, ok := submitted.Load().(map[string]any)
	require.True(t, ok, "no command submission captured")
	assert.Equal(t,
		"The host clock is 40s ahead, so the renewed cert reads as not-yet-valid.",
		body["purpose"])
}

// TestExecLogsAwaitingPurposeExits7 covers the only sight --detach has of a
// demand: SubmitCommand returns before the verdict, so the status is first
// visible when the result is retrieved.
func TestExecLogsAwaitingPurposeExits7(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+purposeJobID+"/" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": purposeJobID, "status": "awaiting_purpose"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stdout, _, exitCode := runExecLogsHelper(t, ts.URL, "json", purposeJobID)
	assert.Equal(t, utils.ExitCodePurposeRequired, exitCode)

	var got purposeRequiredSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, utils.PurposeRequiredStatus, got.Status)
	assert.Equal(t, purposeJobID, got.CommandID)
	assert.False(t, got.RequiresHumanApproval)
}

const purposeJobID = "a1b2c3d4-5678-abcd-ef01-234567890abc"

// TestAwaitingPurposeStatusIsNotAnApproval keeps the two holds apart at the
// status predicates, which is where a rename would collapse them.
func TestAwaitingPurposeStatusIsNotAnApproval(t *testing.T) {
	assert.True(t, event.IsAwaitingPurposeStatus("awaiting_purpose"))
	assert.False(t, event.IsAwaitingApprovalStatus("awaiting_purpose"))
	assert.False(t, event.IsAwaitingPurposeStatus("awaiting_approval"))
	// Not a running state either: treating it as one would poll a command that
	// is waiting on this caller, until the window closed.
	assert.False(t, event.IsRunningStatus("awaiting_purpose"))
}

// TestPurposeFlagRejectsAnEmptyValue keeps a blank answer a usage error rather
// than a 400 that spends the command's one demand.
func TestPurposeFlagRejectsAnEmptyValue(t *testing.T) {
	parsed := ParseRemoteExecArgs([]string{"--purpose", "   ", "prod", "uptime"})

	assert.Contains(t, parsed.Err, "--purpose requires a value")
}

// TestPurposeFlagRejectsAnOverLongValue mirrors the server ceiling locally, for
// the same reason.
func TestPurposeFlagRejectsAnOverLongValue(t *testing.T) {
	long := make([]byte, PurposeMaxLength+1)
	for i := range long {
		long[i] = 'x'
	}
	parsed := ParseRemoteExecArgs([]string{"--purpose", string(long), "prod", "uptime"})

	assert.Contains(t, parsed.Err, "limited to 2000 characters")
}

// TestPurposeFlagIsParsedInBothForms covers the attached form, which the
// long-name sentinel in extractFlagValue makes non-obvious.
func TestPurposeFlagIsParsedInBothForms(t *testing.T) {
	spaced := ParseRemoteExecArgs([]string{"--purpose", "clock skew", "prod", "uptime"})
	assert.Empty(t, spaced.Err)
	assert.Equal(t, "clock skew", spaced.Purpose)

	attached := ParseRemoteExecArgs([]string{"--purpose=clock skew", "prod", "uptime"})
	assert.Empty(t, attached.Err)
	assert.Equal(t, "clock skew", attached.Purpose)
}

// TestExecPurposeRejectsBadArgsWithExit1 pins the exit code for this command's
// own argument validation. Exit 2 is reserved for work-session, event
// wait/watch, and utils.RequirePositiveInt (README "Exit codes"); every other
// command exits 1, `exec logs` included. Exiting 2 here would tell a script this
// is a usage-error-only surface, which it is not.
func TestExecPurposeRejectsBadArgsWithExit1(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"not a uuid", []string{"not-a-uuid", "clock skew"}, "invalid JOB_ID"},
		{"blank purpose", []string{purposeJobID, "   "}, "PURPOSE cannot be empty"},
		{"over the ceiling", []string{purposeJobID, longPurpose()}, "limited to 2000 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, exitCode := runExecPurposeHelper(t, ts.URL, tc.args...)
			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assert.Contains(t, stderr, tc.want)
		})
	}
}

// TestExecPurposeAnswersTheDemand covers the happy path: a 202 and a pointer at
// where to read the outcome, never at a resubmit.
func TestExecPurposeAnswersTheDemand(t *testing.T) {
	var body atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/"+purposeJobID+"/purpose/" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			body.Store(payload)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	home := t.TempDir()
	writeExecCommandTestConfig(t, home, ts.URL)
	stderr := runExecPurposeOK(t, home, purposeJobID, "chronyd drifted 40s.")

	sent, ok := body.Load().(map[string]any)
	require.True(t, ok, "no purpose answer captured")
	assert.Equal(t, "chronyd drifted 40s.", sent["purpose"])
	assert.Contains(t, stderr, "Purpose recorded")
	assert.Contains(t, stderr, "alpacon exec logs "+purposeJobID)
	assert.NotContains(t, stderr, "re-run")
}

func longPurpose() string {
	b := make([]byte, PurposeMaxLength+1)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
