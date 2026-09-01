package exec

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end reporting of a purpose demand through the real `exec` and
// `exec logs` commands. The envelope's own shape is pinned next to the code that
// builds it, in utils/purpose_demand_test.go; `exec purpose` lives in
// purpose_test.go.

// demandSignal is the slice of the envelope these end-to-end tests branch on.
// The full contract is asserted in utils/purpose_demand_test.go.
type demandSignal struct {
	Status                string             `json:"status"`
	ExitCode              int                `json:"exit_code"`
	CommandID             string             `json:"command_id"`
	RequiresHumanApproval bool               `json:"requires_human_approval"`
	AnswerableByCaller    bool               `json:"answerable_by_caller"`
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
				"id":                 "cmd-1",
				"status":             "awaiting_purpose",
				"purpose_expires_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
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

	var got demandSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
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
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                 purposeJobID,
				"status":             "awaiting_purpose",
				"purpose_expires_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stdout, _, exitCode := runExecLogsHelper(t, ts.URL, "json", purposeJobID)
	assert.Equal(t, utils.ExitCodePurposeRequired, exitCode)

	var got demandSignal
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.Equal(t, utils.PurposeRequiredStatus, got.Status)
	assert.Equal(t, purposeJobID, got.CommandID)
	assert.False(t, got.RequiresHumanApproval)
}

// TestRunExecWithApprovalWait_DoesNotWaitOnAPurposeDemand pins the decision the
// PR description argues at length.
//
// The demand reaches the caller today only because the PendingApprovalError
// branch and isApprovalDenial both happen to miss AwaitingPurposeError. Fold it
// into either one and --wait sleeps through the whole ~60s window instead of
// reporting it—spending the command's single chance on a wait that cannot end
// well, since the answer was this caller's to give all along.
func TestRunExecWithApprovalWait_DoesNotWaitOnAPurposeDemand(t *testing.T) {
	_ = stubApprovalWaitSeams(t, 10*time.Millisecond, "SUDO_APPROVAL_REQUIRED")
	demand := &event.AwaitingPurposeError{CommandID: "cmd-1"}
	calls := 0
	runPresenceStepUp = func(*client.AlpaconClient, string, string, string, string, map[string]string, string, string, io.Writer) error {
		calls++
		return demand
	}

	start := time.Now()
	err := RunExecWithApprovalWait(nil, "srv", "whoami", "", "", nil, "", "", time.Minute, io.Discard)

	assert.Same(t, demand, err, "the demand must reach the caller unchanged")
	assert.Equal(t, 1, calls, "a purpose demand must not be retried inside the wait loop")
	assert.Less(t, time.Since(start), time.Second, "the wait window must not be entered at all")
}

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
