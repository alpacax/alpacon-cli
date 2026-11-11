package exec

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const assigneeJobID = "550e8400-e29b-41d4-a716-446655440000"

// newHeldCommandServer answers exec and exec logs with a command held at
// awaiting_approval. laneField is the raw approval_lane member, empty for an
// older server that sends none.
func newHeldCommandServer(t *testing.T, laneField string) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		detail := func(id string) {
			_, _ = fmt.Fprintf(w, `{"id":%q,"status":"awaiting_approval","console_url":%q%s}`,
				id, ts.URL+"/commands/"+id, laneField)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"cmd-1"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			detail("cmd-1")
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+assigneeJobID+"/":
			detail(assigneeJobID)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

const (
	assigneeBannerTitle = "==== ALPACON: CONFIRMATION PENDING ===="
	approverBannerTitle = "==== ALPACON: APPROVAL PENDING ===="
)

func TestExecHeldCommandNamesWhoConfirms(t *testing.T) {
	for _, tc := range []struct {
		name         string
		laneField    string
		wantAssignee bool
	}{
		{"assignee", `,"approval_lane":"assignee"`, true},
		{"approver lane", `,"approval_lane":"single_approver"`, false},
		{"older server", ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newHeldCommandServer(t, tc.laneField)

			_, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "sudo", "reboot")
			assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
			if tc.wantAssignee {
				assert.Contains(t, stderr, assigneeBannerTitle+"\nsudo on prod is waiting for your own confirmation, not an approver's. Confirm or reject it in the Alpacon web console.\n"+ts.URL+"/commands/cmd-1\n")
				assert.Contains(t, stderr, "Confirmation required—this command is held for your own confirmation as the session's assignee, not for an approver.")
				assert.NotContains(t, stderr, "Approval required")
				assert.NotContains(t, stderr, approverBannerTitle)
				return
			}
			assert.Contains(t, stderr, approverBannerTitle)
			assert.Contains(t, stderr, "Approval required—this command is held for human approval in the Alpacon console (web).")
			assert.NotContains(t, stderr, "your own confirmation")
		})
	}
}

func TestExecLogsHeldCommandNamesWhoConfirms(t *testing.T) {
	for _, tc := range []struct {
		name         string
		laneField    string
		wantAssignee bool
	}{
		{"assignee", `,"approval_lane":"assignee"`, true},
		{"approver lane", `,"approval_lane":"single_approver"`, false},
		{"older server", ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newHeldCommandServer(t, tc.laneField)

			_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, assigneeJobID)
			assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
			if tc.wantAssignee {
				assert.Contains(t, stderr, assigneeBannerTitle)
				assert.Contains(t, stderr, "Confirmation required—this command is awaiting your own confirmation as the session's assignee, not an approver's, in the Alpacon web console; status awaiting_approval. It runs automatically once confirmed.")
				assert.NotContains(t, stderr, "Approval required")
				return
			}
			assert.Contains(t, stderr, approverBannerTitle)
			assert.Contains(t, stderr, "Approval required—this command is awaiting human approval in the Alpacon console (web); status awaiting_approval.")
			assert.NotContains(t, stderr, "your own confirmation")
		})
	}
}

// The lane changes the words only: the envelope keeps its keys, status, exit code
// and next actions, so a consumer branching on them sees no difference.
func TestHeldCommandJSONShapeIsTheSameForBothLanes(t *testing.T) {
	envelope := func(t *testing.T, laneField string, logs bool) map[string]any {
		t.Helper()
		ts := newHeldCommandServer(t, laneField)
		var stdout string
		var exitCode int
		if logs {
			stdout, _, exitCode = runExecLogsHelper(t, ts.URL, utils.OutputFormatJSON, assigneeJobID)
		} else {
			stdout, _, exitCode = runExecHelper(t, ts.URL, "--output", "json", "prod", "--", "sudo", "reboot")
		}
		assert.Equal(t, utils.ExitCodePendingApproval, exitCode)
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
		return got
	}
	for _, tc := range []struct {
		name string
		logs bool
	}{{"exec", false}, {"exec logs", true}} {
		t.Run(tc.name, func(t *testing.T) {
			assignee := envelope(t, `,"approval_lane":"assignee"`, tc.logs)
			approver := envelope(t, ``, tc.logs)

			keys := func(m map[string]any) []string {
				out := make([]string, 0, len(m))
				for k := range m {
					out = append(out, k)
				}
				slices.Sort(out)
				return out
			}
			assert.Equal(t, keys(approver), keys(assignee))
			assert.Equal(t, approver["status"], assignee["status"])
			assert.Equal(t, approver["exit_code"], assignee["exit_code"])
			assert.Equal(t, approver["next_actions"], assignee["next_actions"])
			assert.Contains(t, assignee["message"], "your own confirmation")
			assert.NotContains(t, approver["message"], "your own confirmation")
		})
	}
}

func TestExecLogsRuleChangedRejection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+assigneeJobID+"/" {
			_, _ = fmt.Fprintf(w, `{"id":%q,"status":"rejected","rejection_cause":"approval_rule_changed","rejection_gate":"approval","approval_lane":null}`, assigneeJobID)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, assigneeJobID)
	assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
	assert.Contains(t, stderr, "the approval rule changed while it waited")
	assert.Contains(t, stderr, "Sending the command again applies the rule that holds now.")
	for _, judgment := range []string{"score", "reasoning", "confidence"} {
		assert.NotContains(t, stderr, judgment)
	}
}

// TestExecLogsRuleChangedRejectionSendsNoDeniedSignal pins approval_rule_changed
// as a lapse: nobody refused the command, so no command-denied banner or
// desktop notification goes out.
func TestExecLogsRuleChangedRejectionSendsNoDeniedSignal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+assigneeJobID+"/" {
			_, _ = fmt.Fprintf(w, `{"id":%q,"status":"rejected","rejection_cause":"approval_rule_changed","rejection_gate":"approval","approval_lane":null}`, assigneeJobID)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	logPath := filepath.Join(t.TempDir(), "notifications")
	require.NoError(t, os.WriteFile(logPath, nil, 0600))
	origNotifier := localsignal.NewNotifier
	t.Cleanup(func() { localsignal.NewNotifier = origNotifier })
	localsignal.NewNotifier = func() localsignal.Notifier { return approvalWaitNotifier{path: logPath} }

	_, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, assigneeJobID)
	assert.Equal(t, utils.ExitCodeNotApproved, exitCode)
	assert.Contains(t, stderr, "the approval rule changed while it waited")
	assert.NotContains(t, stderr, "==== ALPACON: COMMAND DENIED ====")
	notifications, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(notifications), "Alpacon: command denied")
}
