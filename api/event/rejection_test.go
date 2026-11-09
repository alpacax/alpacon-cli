package event

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/types"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rejectedDetails(t *testing.T, body string) EventDetails {
	t.Helper()
	var d EventDetails
	require.NoError(t, json.Unmarshal([]byte(`{"id":"cmd-9","status":"rejected"`+body+`}`), &d))
	return d
}

func TestRejectionFieldsDecodeAbsentNullAndValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		body         string
		wantCause    types.SentString
		wantGate     types.SentString
		wantReported bool
	}{
		{name: "absent", body: ``},
		{name: "null", body: `,"rejection_cause":null,"rejection_gate":null`, wantCause: types.SentString{Sent: true}, wantGate: types.SentString{Sent: true}, wantReported: true},
		{name: "value", body: `,"rejection_cause":"risk_denied","rejection_gate":"risk"`, wantCause: types.SentString{Value: "risk_denied", Sent: true}, wantGate: types.SentString{Value: "risk", Sent: true}, wantReported: true},
		{name: "cause only", body: `,"rejection_cause":"risk_denied"`, wantCause: types.SentString{Value: "risk_denied", Sent: true}, wantReported: true},
		{name: "gate only", body: `,"rejection_gate":"risk"`, wantGate: types.SentString{Value: "risk", Sent: true}, wantReported: true},
		{name: "null cause with a gate", body: `,"rejection_cause":null,"rejection_gate":"risk"`, wantCause: types.SentString{Sent: true}, wantGate: types.SentString{Value: "risk", Sent: true}, wantReported: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := rejectedDetails(t, tc.body)
			assert.Equal(t, tc.wantCause, d.RejectionCause)
			assert.Equal(t, tc.wantGate, d.RejectionGate)
			assert.Equal(t, tc.wantReported, d.Reported())
		})
	}
}

func TestRejectionNamesEachCause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cause, gate, wantWhat string
		wantNext              utils.NextAction
	}{
		{"command_acl_denied", "command_acl", "the command ACL did not admit it", utils.NextAction{Description: "The command needs a pre-approved command entry in this workspace; without one it is refused the same way."}},
		{"command_acl_unavailable", "command_acl", "the command ACL's verdict could not be obtained", utils.NextAction{Description: "The command pre-approval ledger gave no verdict; the command did not run."}},
		{"risk_denied", "risk", "the risk assessor refused it", utils.NextAction{Description: "The workspace risk policy refuses this command; the same command is refused the same way."}},
		{"risk_unavailable", "risk", "the risk assessor was unavailable", utils.NextAction{Description: "The risk assessor gave no verdict; the command did not run."}},
		{"approval_policy_denied", "approval", "an approval policy refused it, or no one but the requester could decide it", utils.NextAction{Description: "The approval policy needs an approver other than the requester, or refuses the command outright."}},
		{"approval_rejected", "approval", "an approver rejected it", utils.NextAction{Description: "Ask the approver why; the decision is in the Alpacon console (web)."}},
		{"approval_expired", "approval", "its approval request expired with no decision", utils.NextAction{Description: "Ask an approver why the request went undecided."}},
		{"approval_cancelled", "approval", "the requester cancelled its approval request", utils.NextAction{Description: "Ask the requester why the approval request was withdrawn."}},
		{"work_session_terminated", "work_session", "its work session ended while it was held", utils.NextAction{Command: "alpacon work-session current", Description: "the work session it was held under has ended; this shows the one in use now"}},
		{"account_bound_to_other_person", "", "the account it would run as was linked to another person before it was delivered", utils.NextAction{Description: "Only the person the account is linked to can run commands as it."}},
		{"unsigned_file", "", "the file command reached the agent without a signature", utils.NextAction{Description: "The file needs a signature; the agent runs only signed files."}},
		{"signature_invalid", "", "the agent could not verify the file's signature", utils.NextAction{Description: "The file's signature has to verify; the agent refuses one that does not."}},
	}
	for _, tc := range cases {
		t.Run(tc.cause, func(t *testing.T) {
			t.Parallel()
			body := `,"rejection_cause":"` + tc.cause + `","rejection_gate":null`
			wantTail := "(rejection_cause: " + tc.cause + ")"
			if tc.gate != "" {
				body = `,"rejection_cause":"` + tc.cause + `","rejection_gate":"` + tc.gate + `"`
				wantTail = "(rejection_cause: " + tc.cause + ", rejection_gate: " + tc.gate + ")"
			}

			err := errorFromDetails(testPathID(t, "cmd-9"), rejectedDetails(t, body))

			var rejected *CommandRejectedError
			require.ErrorAs(t, err, &rejected)
			assert.Equal(t, "command cmd-9 was rejected: "+tc.wantWhat+" "+wantTail, err.Error())
			code, _ := utils.ParseErrorResponse(err)
			assert.Equal(t, tc.cause, code)
			assert.Equal(t, []utils.NextAction{tc.wantNext}, rejected.NextActions())
		})
	}
}

func TestNoRejectionTextSuggestsSubmittingTheCommandAgain(t *testing.T) {
	t.Parallel()
	texts := map[string]rejectionText{"unknown cause": unknownCauseText, "cause not recorded": notRecordedText}
	for cause, text := range causeTexts {
		texts["cause "+cause] = text
	}
	for gate, text := range gateTexts {
		texts["gate "+gate] = text
	}

	for name, text := range texts {
		said := strings.ToLower(text.what + " " + text.next.PlainText())
		for _, banned := range []string{"resubmit", "re-run", "rerun", "run it again", "retry", "submit it again", "try again"} {
			assert.NotContains(t, said, banned, name)
		}
	}
}

func TestRejectionFallbacks(t *testing.T) {
	t.Parallel()
	unknown := utils.NextAction{Description: "This CLI does not know the cause yet; the command did not run."}
	cases := []struct {
		name        string
		body        string
		wantError   string
		wantActions []utils.NextAction
	}{
		{
			name:        "unknown cause falls back to its gate",
			body:        `,"rejection_cause":"new_cause","rejection_gate":"risk"`,
			wantError:   "command cmd-9 was rejected: the risk assessor refused it (rejection_cause: new_cause, rejection_gate: risk)",
			wantActions: []utils.NextAction{{Description: "The workspace risk policy refuses this command."}},
		},
		{
			name:        "unknown cause on the work session gate points at the current session",
			body:        `,"rejection_cause":"new_cause","rejection_gate":"work_session"`,
			wantError:   "command cmd-9 was rejected: its work session refused it (rejection_cause: new_cause, rejection_gate: work_session)",
			wantActions: []utils.NextAction{{Command: "alpacon work-session current", Description: "shows the work session in use now"}},
		},
		{
			name:        "unknown cause and unknown gate",
			body:        `,"rejection_cause":"new_cause","rejection_gate":"new_gate"`,
			wantError:   "command cmd-9 was rejected: the server refused it (rejection_cause: new_cause, rejection_gate: new_gate)",
			wantActions: []utils.NextAction{unknown},
		},
		{
			name:        "unknown cause and null gate",
			body:        `,"rejection_cause":"new_cause","rejection_gate":null`,
			wantError:   "command cmd-9 was rejected: the server refused it (rejection_cause: new_cause)",
			wantActions: []utils.NextAction{unknown},
		},
		{
			name:        "unknown cause with no gate key",
			body:        `,"rejection_cause":"new_cause"`,
			wantError:   "command cmd-9 was rejected: the server refused it (rejection_cause: new_cause)",
			wantActions: []utils.NextAction{unknown},
		},
		{
			name:        "null cause means the server did not record why",
			body:        `,"rejection_cause":null,"rejection_gate":null`,
			wantError:   "command cmd-9 was rejected; the server did not record why",
			wantActions: []utils.NextAction{{Description: "Check the command in the Alpacon console (web); the cause was not recorded."}},
		},
		{
			name:        "gate only means the server did not record why",
			body:        `,"rejection_gate":"risk"`,
			wantError:   "command cmd-9 was rejected; the server did not record why",
			wantActions: []utils.NextAction{{Description: "Check the command in the Alpacon console (web); the cause was not recorded."}},
		},
		{
			name:        "null cause with a gate means the server did not record why",
			body:        `,"rejection_cause":null,"rejection_gate":"risk"`,
			wantError:   "command cmd-9 was rejected; the server did not record why",
			wantActions: []utils.NextAction{{Description: "Check the command in the Alpacon console (web); the cause was not recorded."}},
		},
		{
			name:        "older server sends neither key and gets the same answer",
			body:        ``,
			wantError:   "command cmd-9 was rejected; the server did not record why",
			wantActions: []utils.NextAction{{Description: "Check the command in the Alpacon console (web); the cause was not recorded."}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := errorFromDetails(testPathID(t, "cmd-9"), rejectedDetails(t, tc.body))

			var rejected *CommandRejectedError
			require.ErrorAs(t, err, &rejected)
			assert.Equal(t, tc.wantError, err.Error())
			assert.Equal(t, tc.wantActions, rejected.NextActions())
			code, _ := utils.ParseErrorResponse(err)
			assert.Equal(t, rejected.Cause, code, "error_code is the cause the server sent, empty for none")
		})
	}
}

func TestRejectionErrorFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want map[string]any
	}{
		{name: "cause and gate", body: `,"rejection_cause":"risk_denied","rejection_gate":"risk"`, want: map[string]any{"rejection_cause": "risk_denied", "rejection_gate": "risk"}},
		{name: "signature cause has a null gate", body: `,"rejection_cause":"unsigned_file","rejection_gate":null`, want: map[string]any{"rejection_cause": "unsigned_file", "rejection_gate": nil}},
		{name: "cause only", body: `,"rejection_cause":"risk_denied"`, want: map[string]any{"rejection_cause": "risk_denied", "rejection_gate": nil}},
		{name: "gate only", body: `,"rejection_gate":"risk"`, want: map[string]any{"rejection_cause": nil, "rejection_gate": "risk"}},
		{name: "null cause with a gate", body: `,"rejection_cause":null,"rejection_gate":"risk"`, want: map[string]any{"rejection_cause": nil, "rejection_gate": "risk"}},
		{name: "both null", body: `,"rejection_cause":null,"rejection_gate":null`, want: map[string]any{"rejection_cause": nil, "rejection_gate": nil}},
		{name: "older server sends neither", body: ``, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, rejectedDetails(t, tc.body).Rejection().ErrorFields())
		})
	}
}

func TestRejectionErrorSanitizesServerText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		rejected *CommandRejectedError
	}{
		{name: "cause and gate", rejected: &CommandRejectedError{CommandID: "cmd-9", Cause: "x\x1b[31mred", Gate: "g\x1b[2J", Reported: true}},
		{name: "command id", rejected: &CommandRejectedError{CommandID: "cmd\x1b[2J-9\x07", Cause: "risk_denied", Reported: true}},
		{name: "command id without a cause", rejected: &CommandRejectedError{CommandID: "cmd\x1b[2J-9", Reported: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg := tc.rejected.Error()
			assert.NotContains(t, msg, "\x1b")
			assert.NotContains(t, msg, "\x07")
		})
	}
}

func TestRejectionLabel(t *testing.T) {
	t.Parallel()
	label := func(cause, gate string) string {
		return CommandRejection{
			RejectionCause: types.SentString{Value: cause, Sent: true},
			RejectionGate:  types.SentString{Value: gate, Sent: true},
		}.Label()
	}
	assert.Equal(t, "rejected: risk_denied, gate risk", label("risk_denied", "risk"))
	assert.Equal(t, "rejected: unsigned_file", label("unsigned_file", ""))
	assert.Equal(t, "rejected: cause not recorded", label("", ""))
	assert.Equal(t, "rejected: cause not recorded", label("", "risk"))
	assert.Equal(t, "rejected: xred, gate g", label("x\x1b[31mred", "g\x1b[2J"))
}

func TestSudoGrantRefusalKeepsTheReviewerWording(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		rejected  *CommandRejectedError
		wantError string
	}{
		{"rejected", &CommandRejectedError{CommandID: "cmd-9", SudoGrant: true}, "command cmd-9 was rejected by a reviewer"},
		{"expired", &CommandRejectedError{CommandID: "cmd-9", SudoGrant: true, Expired: true}, "command cmd-9 was not approved before the approval request expired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg := tc.rejected.Error()

			assert.Equal(t, tc.wantError, msg)
			assert.Nil(t, tc.rejected.NextActions())
			assert.Nil(t, tc.rejected.ErrorFields())
		})
	}
}

func TestIsRejectedReadsTheStatusAndACompletedCommandsCause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		detail string
		want   bool
	}{
		{"rejected status", `{"status":"rejected"}`, true},
		{"agent refused the signature after delivery", `{"status":"completed","success":false,"rejection_cause":"signature_invalid","rejection_gate":null}`, true},
		{"completed with a null cause", `{"status":"completed","success":false,"rejection_cause":null}`, false},
		{"completed with another cause keeps its result", `{"status":"completed","success":true,"rejection_cause":"unsigned_file"}`, false},
		{"denied with a cause is a refusal", `{"status":"denied","rejection_cause":"account_bound_to_other_person"}`, true},
		{"running", `{"status":"running"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var d EventDetails
			require.NoError(t, json.Unmarshal([]byte(tc.detail), &d))

			got := d.IsRejected()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAReasonThatSanitizesToNothingKeepsTheAskTheApproverStep(t *testing.T) {
	t.Parallel()
	for name, note := range map[string]string{"whitespace only": `  \n `, "escape only": `\u001b[2K`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := `,"rejection_cause":"approval_rejected","rejection_gate":"approval","rejection_note":"` + note + `"`

			rejected := rejectedDetails(t, body).Rejection()

			assert.Equal(t, []utils.NextAction{causeTexts[RejectionCauseApprovalRejected].next}, rejected.NextActions())
			assert.Contains(t, rejected.ErrorFields(), "rejection_note")
		})
	}
}

func TestAnApproverReasonTakesThePlaceOfAskTheApprover(t *testing.T) {
	t.Parallel()
	body := `,"rejection_cause":"approval_rejected","rejection_gate":"approval","rejection_note":"Add a guard\non line 23.\u001b[2K"`

	err := errorFromDetails(testPathID(t, "cmd-9"), rejectedDetails(t, body))

	var rejected *CommandRejectedError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "command cmd-9 was rejected: an approver rejected it (rejection_cause: approval_rejected, rejection_gate: approval)", err.Error())
	assert.Equal(t, []utils.NextAction{{Description: `The approver wrote: "Add a guard on line 23."`}}, rejected.NextActions())
	assert.Equal(t, "Add a guard\non line 23.\u001b[2K", rejected.ErrorFields()["rejection_note"])
}

func TestNoReasonKeepsTheAskTheApproverStep(t *testing.T) {
	t.Parallel()
	for name, note := range map[string]string{"absent": ``, "null": `,"rejection_note":null`, "empty": `,"rejection_note":""`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := `,"rejection_cause":"approval_rejected","rejection_gate":"approval"` + note

			rejected := rejectedDetails(t, body).Rejection()

			assert.Equal(t, []utils.NextAction{causeTexts[RejectionCauseApprovalRejected].next}, rejected.NextActions())
			assert.NotContains(t, rejected.ErrorFields(), "rejection_note")
		})
	}
}

func TestDeniedCommandIsATerminalRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{"known denial code", `{"status":"denied","denial_code":"account_not_resolved","result":"The execution account could not be resolved."}`},
		{"unknown denial code", `{"status":"denied","denial_code":"future_reason"}`},
		{"no reason", `{"status":"denied"}`},
		{"null denial code with rejection cause", `{"status":"denied","denial_code":null,"rejection_cause":"signature_invalid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var details EventDetails
			require.NoError(t, json.Unmarshal([]byte(tc.body), &details))

			err := errorFromDetails(testPathID(t, "cmd-1"), details)

			var refusal *CommandRejectedError
			require.ErrorAs(t, err, &refusal)
			assert.Equal(t, utils.ExitCodeNotApproved, refusal.ExitCode())
			assert.Contains(t, err.Error(), "did not run")
			assert.NotContains(t, err.Error(), "may still be running")
		})
	}
}
