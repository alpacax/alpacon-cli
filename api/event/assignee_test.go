package event

import (
	"encoding/json"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approval_lane names who decides a held command; only "assignee" makes the
// confirmation the requester's own, and an older server sends no field at all.
func TestHeldCommandCarriesTheApprovalLane(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		lane         string
		wantLane     string
		wantAssignee bool
	}{
		{"assignee", `,"approval_lane":"assignee"`, "assignee", true},
		{"approver lane", `,"approval_lane":"single_approver"`, "single_approver", false},
		{"null", `,"approval_lane":null`, "", false},
		{"older server", ``, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var d EventDetails
			require.NoError(t, json.Unmarshal([]byte(`{"id":"cmd-9","status":"awaiting_approval"`+tc.lane+`}`), &d))

			err := errorFromDetails(testPathID(t, "cmd-9"), d)

			var pending *PendingApprovalError
			require.ErrorAs(t, err, &pending)
			assert.Equal(t, tc.wantLane, pending.ApprovalLane)
			assert.Equal(t, tc.wantAssignee, pending.AssigneeConfirms())
			assert.Nil(t, pending.ErrorFields(), "the lane never reaches the JSON error fields")
		})
	}
}

// A held command closed because the rule changed while it waited is judged
// again under the current rule when sent again, so that is the step.
func TestRuleChangedRejectionSaysSendingItAgainAppliesTheCurrentRule(t *testing.T) {
	t.Parallel()
	err := errorFromDetails(testPathID(t, "cmd-9"), rejectedDetails(t, `,"rejection_cause":"approval_rule_changed","rejection_gate":"approval"`))

	var rejected *CommandRejectedError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "command cmd-9 was rejected: the approval rule changed while it waited (rejection_cause: approval_rule_changed, rejection_gate: approval)", err.Error())
	assert.Equal(t, []utils.NextAction{{Description: "Sending the command again applies the rule that holds now."}}, rejected.NextActions())
	assert.Equal(t, utils.ExitCodeNotApproved, rejected.ExitCode())
	code, _ := utils.ParseErrorResponse(err)
	assert.Equal(t, RejectionCauseApprovalRuleChanged, code)
}
