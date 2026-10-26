package utils

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsWorkSessionCode(t *testing.T) {
	t.Parallel()
	trueCodes := []string{
		WorkSessionRequired,
		WorkSessionNotUsable,
		WorkSessionNotActive,
		WorkSessionExpired,
		WorkSessionScopeNotAllowed,
		WorkSessionServerNotAllowed,
		WorkSessionAssigneeMismatch,
	}
	for _, code := range trueCodes {
		assert.True(t, isWorkSessionCode(code), "expected true for %s", code)
	}

	falseCodes := []string{AuthMFARequired, UsernameRequired, "", "some_other_error"}
	for _, code := range falseCodes {
		assert.False(t, isWorkSessionCode(code), "expected false for %s", code)
	}
}

func TestBuildWorkSessionDiagnostic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code          string
		wantReason    string
		wantNext      string
		hasCreate     bool // suggests 'create --use', which must carry an expiry flag
		reuseVsCreate bool // distinguishes reuse (use) vs create-and-attach paths
	}{
		{WorkSessionRequired, "no WorkSession selected", "work-session ls", true, true},
		{WorkSessionNotActive, "not active", "work-session current", true, true},
		{WorkSessionExpired, "has expired", "work-session extend", true, false},
		{WorkSessionScopeNotAllowed, "does not include this scope", "work-session create", true, false},
		{WorkSessionServerNotAllowed, "target server is not in this session", "work-session create", true, false},
		{WorkSessionAssigneeMismatch, "assigned to another principal", "work-session use", true, true},
		{WorkSessionNotUsable, "no longer usable", "work-session create", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := buildWorkSessionDiagnostic(tt.code, "websh", "prod-1", "Browser login", "")
			assert.Contains(t, got, "the websh operation requires an active WorkSession")
			assert.Contains(t, got, tt.wantReason)
			assert.Contains(t, got, tt.wantNext)
			assert.Contains(t, got, "required scope")
			assert.Contains(t, got, "prod-1")
			assert.Contains(t, got, "Browser login (interactive)")
			assert.Contains(t, got, "Note:")
			if tt.hasCreate {
				assert.Contains(t, got, "--expires-in")
				assert.Contains(t, got, "--use")
			}
			if tt.reuseVsCreate {
				assert.Contains(t, got, "alpacon work-session use <ID>")
				assert.Contains(t, got, "existing active session") // reuse path comes first
				assert.Contains(t, got, "create a new one")        // create is the fallback
			}
		})
	}
}

func TestBuildWorkSessionDiagnostic_APIToken(t *testing.T) {
	t.Parallel()
	got := buildWorkSessionDiagnostic(WorkSessionRequired, "command", "srv-1", "API token", "")
	assert.Contains(t, got, "API token")
	assert.NotContains(t, got, "(interactive)")
}

func TestBuildWorkSessionDiagnostic_TokenNoteMatchesOperation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation string
		want      string
		notWant   string
	}{
		{"interactive Websh needs a login without a token", OperationWebsh, "cannot open an interactive terminal; it needs 'alpacon login' without -t (on a machine without a browser, open the printed URL on another device)", "bypass this check"},
		{"command is bypassed by a token", OperationCommand, "bypass this check", "cannot open an interactive terminal"},
		{"webftp is bypassed by a token", OperationWebFTP, "bypass this check", "cannot open an interactive terminal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildWorkSessionDiagnostic(WorkSessionRequired, tt.operation, "prod-1", "Browser login", "")
			assert.Contains(t, got, tt.want)
			assert.NotContains(t, got, tt.notWant)
		})
	}
}

func TestBuildWorkSessionErrorEnvelope(t *testing.T) {
	t.Parallel()
	tests := []string{
		WorkSessionRequired,
		WorkSessionNotActive,
		WorkSessionExpired,
		WorkSessionScopeNotAllowed,
		WorkSessionServerNotAllowed,
		WorkSessionAssigneeMismatch,
		WorkSessionNotUsable,
	}

	for _, code := range tests {
		t.Run(code, func(t *testing.T) {
			envelope := buildWorkSessionErrorEnvelope(code, "command", "srv-1", "API token", "")

			assert.False(t, envelope.OK)
			assert.Equal(t, ExitCodeWorkSessionDenied, envelope.ExitCode)
			assert.Equal(t, code, envelope.ErrorCode)
			assert.Equal(t, "the command operation requires an active WorkSession on this authentication.", envelope.Message)
			assert.Equal(t, workSessionReasonMap[code], envelope.Reason)
			assert.Equal(t, "command", envelope.Context.RequiredScope)
			assert.Equal(t, []string{"srv-1"}, envelope.Context.TargetServers)
			assert.Nil(t, envelope.Context.CurrentWorksession)
			assert.NotEmpty(t, envelope.NextActions)
		})
	}
}

func TestBuildWorkSessionErrorEnvelope_WithActiveWS(t *testing.T) {
	t.Parallel()
	envelope := buildWorkSessionErrorEnvelope(WorkSessionExpired, "webftp", "srv-2", "Browser login", "abc-123-uuid")

	assert.NotNil(t, envelope.Context.CurrentWorksession)
	assert.Equal(t, "abc-123-uuid", *envelope.Context.CurrentWorksession)
	// Expired case: <ID> placeholder must be substituted with the known active UUID.
	assert.Contains(t, nextActionCommands(envelope.NextActions), "alpacon work-session extend abc-123-uuid")
	for _, action := range envelope.NextActions {
		assert.NotContains(t, action.Command, "<ID>")
	}
}

func TestBuildWorkSessionErrorEnvelope_WebshAgentActionAvoidsWebshScope(t *testing.T) {
	t.Parallel()
	codes := []string{
		WorkSessionRequired,
		WorkSessionNotActive,
		WorkSessionExpired,
		WorkSessionScopeNotAllowed,
		WorkSessionServerNotAllowed,
		WorkSessionAssigneeMismatch,
		WorkSessionNotUsable,
	}

	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			t.Parallel()

			envelope := buildWorkSessionErrorEnvelope(code, OperationWebsh, "srv-1", "Browser login", "")

			agentCmd := `alpacon work-session create --scope command --server srv-1 --expires-in 1h --purpose "<intent>" --requester-type agent`
			assert.Contains(t, nextActionCommands(envelope.NextActions), agentCmd)
			assert.NotContains(t, nextActionCommands(envelope.NextActions),
				`alpacon work-session create --scope websh --server srv-1 --expires-in 1h --purpose "<intent>" --requester-type agent`)
			for _, action := range envelope.NextActions {
				if action.Command == agentCmd {
					assert.Contains(t, action.Description, "alpacon exec --work-session <ID>")
				}
			}
		})
	}
}

func nextActionCommands(actions []NextAction) []string {
	cmds := make([]string, len(actions))
	for i, a := range actions {
		cmds[i] = a.Command
	}
	return cmds
}

func TestBuildWorkSessionErrorEnvelope_ExpiredWithoutActiveWS(t *testing.T) {
	t.Parallel()
	// When activeWS is unknown, the placeholder must remain.
	envelope := buildWorkSessionErrorEnvelope(WorkSessionExpired, "webftp", "srv-2", "Browser login", "")

	assert.Contains(t, nextActionCommands(envelope.NextActions), "alpacon work-session extend <ID>")
}

func TestBuildWorkSessionErrorEnvelope_RequiredKeepsPlaceholder(t *testing.T) {
	t.Parallel()
	// For work_session_required, activeWS is unrelated to the suggested `use <ID>`,
	// so the placeholder must NOT be substituted even when activeWS is known.
	envelope := buildWorkSessionErrorEnvelope(WorkSessionRequired, "command", "srv-1", "Browser login", "abc-123-uuid")

	assert.Contains(t, nextActionCommands(envelope.NextActions), "alpacon work-session use <ID>")
}

func TestHandleWorkSessionError_NoOp(t *testing.T) {
	t.Parallel()
	// Non-WorkSession errors must not trigger any exit — just return.
	// If HandleWorkSessionError calls os.Exit for this error, the test process dies.
	err := errors.New("some unrelated error")
	HandleWorkSessionError(err, "websh", "srv", "Browser login", "")
	// reaching here means no exit — test passes
}

func TestHandleWorkSessionError_NilError(t *testing.T) {
	t.Parallel()
	HandleWorkSessionError(nil, "websh", "srv", "Browser login", "")
}

func kubeWorkSessionCases() []struct {
	code     string
	activeWS string
	want     []string
} {
	const (
		ls          = "alpacon work-session ls --status active"
		login       = "alpacon kube login prod-east"
		agentCreate = `alpacon work-session create --scope kube --cluster prod-east --expires-in 1h --purpose "<intent>" --requester-type agent`
	)
	reuseOrCreate := []string{ls, login, agentCreate}
	return []struct {
		code     string
		activeWS string
		want     []string
	}{
		{WorkSessionRequired, "", reuseOrCreate},
		{WorkSessionNotUsable, "", reuseOrCreate},
		{WorkSessionAssigneeMismatch, "", reuseOrCreate},
		{WorkSessionNotActive, "", []string{"alpacon kube login prod-east --work-session <ID>", ls, login, agentCreate}},
		{WorkSessionNotActive, "abc-123-uuid", []string{"alpacon kube login prod-east --work-session abc-123-uuid", ls, login, agentCreate}},
		{WorkSessionExpired, "", []string{"alpacon work-session extend <ID>", login, agentCreate}},
		{WorkSessionExpired, "abc-123-uuid", []string{"alpacon work-session extend abc-123-uuid", login, agentCreate}},
		{WorkSessionScopeNotAllowed, "", reuseOrCreate},
		{WorkSessionServerNotAllowed, "", reuseOrCreate},
		{WorkSessionClusterNotAllowed, "", reuseOrCreate},
		{WorkSessionAccountNotApproved, "", reuseOrCreate},
		{WorkSessionAgentSessionRequired, "", []string{"alpacon work-session ls --status active --requester-type agent", agentCreate}},
	}
}

func kubeCaseName(code, activeWS string) string {
	if activeWS != "" {
		return code + "_with_active_session"
	}
	return code
}

// diagnosticNextCommands returns the commands under "Next:", in order, without their descriptions.
func diagnosticNextCommands(t *testing.T, diagnostic string) []string {
	t.Helper()
	_, block, found := strings.Cut(diagnostic, "\nNext:\n")
	require.True(t, found, "diagnostic has no Next block: %q", diagnostic)
	block, _, _ = strings.Cut(block, "\n\n")
	var cmds []string
	for line := range strings.SplitSeq(block, "\n") {
		cmd, _, _ := strings.Cut(strings.TrimPrefix(line, "  "), "  # ")
		cmds = append(cmds, cmd)
	}
	return cmds
}

func TestBuildWorkSessionDiagnostic_KubeWithoutClusterKeepsPlaceholder(t *testing.T) {
	t.Parallel()
	got := buildWorkSessionDiagnostic(WorkSessionRequired, OperationKube, "", "Browser login", "")
	assert.NotContains(t, got, "target cluster")
	assert.Equal(t, []string{
		"alpacon work-session ls --status active",
		"alpacon kube login <CLUSTER>",
		`alpacon work-session create --scope kube --cluster <CLUSTER> --expires-in 1h --purpose "<intent>" --requester-type agent`,
	}, diagnosticNextCommands(t, got))
}

func TestKubeWorkSessionCasesCoverEveryCode(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, tc := range kubeWorkSessionCases() {
		covered[tc.code] = true
	}
	for code := range workSessionReasonMap {
		assert.True(t, covered[code], "no kube case for %s", code)
	}
}

func TestBuildWorkSessionDiagnostic_Kube(t *testing.T) {
	t.Parallel()
	for _, tc := range kubeWorkSessionCases() {
		t.Run(kubeCaseName(tc.code, tc.activeWS), func(t *testing.T) {
			t.Parallel()
			got := buildWorkSessionDiagnostic(tc.code, OperationKube, "prod-east", "Browser login", tc.activeWS)

			lines := strings.Split(got, "\n")
			require.NotEmpty(t, lines)
			assert.Equal(t, Red("Error")+": opening a kube session requires an active WorkSession that targets the cluster with the kube scope.", lines[0])
			assert.Contains(t, got, workSessionReasonMap[tc.code])
			assert.Contains(t, got, "Browser login (interactive)")
			assert.Contains(t, got, "  required scope: kube\n")
			assert.Contains(t, got, "  target cluster: prod-east\n")
			assert.NotContains(t, got, "  target server :")
			assert.NotContains(t, got, "--server")
			assert.NotContains(t, got, "work-session use")
			assert.NotContains(t, got, "the kube operation")
			assert.Equal(t, tc.want, diagnosticNextCommands(t, got))
			assert.Equal(t, "Note: Tokens issued by Alpacon (service or personal API token) cannot open a kube session; it needs 'alpacon login' without -t (on a machine without a browser, open the printed URL on another device).", lines[len(lines)-1])
		})
	}
}

func TestBuildWorkSessionErrorEnvelope_Kube(t *testing.T) {
	t.Parallel()
	for _, tc := range kubeWorkSessionCases() {
		t.Run(kubeCaseName(tc.code, tc.activeWS), func(t *testing.T) {
			t.Parallel()
			envelope := buildWorkSessionErrorEnvelope(tc.code, OperationKube, "prod-east", "Browser login", tc.activeWS)

			assert.False(t, envelope.OK)
			assert.Equal(t, ExitCodeWorkSessionDenied, envelope.ExitCode)
			assert.Equal(t, tc.code, envelope.ErrorCode)
			assert.Equal(t, "opening a kube session requires an active WorkSession that targets the cluster with the kube scope.", envelope.Message)
			assert.Equal(t, workSessionReasonMap[tc.code], envelope.Reason)
			assert.Equal(t, OperationKube, envelope.Context.RequiredScope)
			assert.Equal(t, []string{}, envelope.Context.TargetServers)
			assert.Equal(t, []string{"prod-east"}, envelope.Context.TargetClusters)
			if tc.activeWS == "" {
				assert.Nil(t, envelope.Context.CurrentWorksession)
			} else if assert.NotNil(t, envelope.Context.CurrentWorksession) {
				assert.Equal(t, tc.activeWS, *envelope.Context.CurrentWorksession)
			}
			assert.Equal(t, tc.want, nextActionCommands(envelope.NextActions))
		})
	}
}

// agentCreateAction returns the recommended agent-session create, which every
// work-session code offers.
func agentCreateAction(t *testing.T, actions []NextAction) NextAction {
	t.Helper()
	for _, a := range actions {
		if strings.HasPrefix(a.Command, "alpacon work-session create ") && strings.Contains(a.Command, "--requester-type agent") {
			return a
		}
	}
	t.Fatalf("no agent create among %v", actions)
	return NextAction{}
}

// An agent session cannot hold websh, so the recovery for websh never asks for
// that combination; it offers a command-scoped session run through exec.
func TestWorkSessionNextActions_AgentRecoveryAvoidsForbiddenScope(t *testing.T) {
	t.Parallel()
	for code := range workSessionReasonMap {
		if code == WorkSessionAccountNotApproved { // offers -u and --account, covered by its own test
			continue
		}
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			actions := workSessionNextActions(code, OperationWebsh, "prod-1", "")
			agent := agentCreateAction(t, actions)
			assert.Contains(t, agent.Command, "--scope "+OperationCommand+" ")
			assert.NotContains(t, agent.Command, "--scope "+OperationWebsh)
			assert.Contains(t, agent.Description, "alpacon exec --work-session <ID>")
			for _, a := range actions {
				if strings.Contains(a.Command, "--requester-type agent") {
					assert.NotContains(t, a.Command, OperationWebsh)
				}
			}
		})
	}

	got := buildWorkSessionDiagnostic(WorkSessionAgentSessionRequired, OperationWebsh, "prod-1", "Browser login", "")
	assert.Contains(t, got, "--scope command --server prod-1")
	assert.NotContains(t, got, "--scope websh")
	assert.Contains(t, got, "an agent session cannot hold websh")
}

func TestWorkSessionNextActions_AgentRecoveryKeepsAllowedScope(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{OperationCommand, OperationWebFTP} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			actions := workSessionNextActions(WorkSessionAgentSessionRequired, operation, "prod-1", "")
			assert.Equal(t, []NextAction{
				{Command: "alpacon work-session ls --status active --requester-type agent", Description: "find an active agent session; reuse it by prefixing the gated command with --work-session <ID>"},
				{Command: `alpacon work-session create --scope ` + operation + ` --server prod-1 --expires-in 1h --purpose "<intent>" --requester-type agent`, Description: "none active? create an agent session (prefix the gated command with --work-session <ID>)"},
			}, actions)
			for code := range workSessionReasonMap {
				if code == WorkSessionAccountNotApproved { // offers -u and --account, covered by its own test
					continue
				}
				agent := agentCreateAction(t, workSessionNextActions(code, operation, "prod-1", ""))
				assert.Contains(t, agent.Command, "--scope "+operation+" ")
				assert.NotContains(t, agent.Description, "alpacon exec")
			}
		})
	}
}

func TestBuildWorkSessionErrorEnvelope_TargetClustersOnlyForKube(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(buildWorkSessionErrorEnvelope(WorkSessionRequired, OperationCommand, "web-01", "Browser login", ""))
	require.NoError(t, err)
	assert.NotContains(t, string(body), "target_clusters")

	body, err = json.Marshal(buildWorkSessionErrorEnvelope(WorkSessionRequired, OperationKube, "", "Browser login", ""))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"target_servers":[]`)
	assert.NotContains(t, string(body), "target_clusters")
}

// An agent session reaches a kube session too, so the recovery offers one; a
// person is pointed at 'alpacon kube login', which picks or creates a session
// without touching the active one.
func TestKubeWorkSessionNextActions_Descriptions(t *testing.T) {
	t.Parallel()
	actions := workSessionNextActions(WorkSessionRequired, OperationKube, "prod-east", "")
	require.Len(t, actions, 3)
	assert.Equal(t, "find an active session for this cluster; pass it with 'alpacon kube login prod-east --work-session <ID>'", actions[0].Description)
	assert.Equal(t, "find or create a work session for this cluster", actions[1].Description)
	assert.Equal(t, "AI agent: create an agent session, then run 'alpacon kube login prod-east --work-session <ID>'", actions[2].Description)
}

func TestKubeWorkSessionAgentSessionRequired(t *testing.T) {
	t.Setenv("AI_AGENT", "test-agent")
	got := buildWorkSessionDiagnostic(WorkSessionAgentSessionRequired, OperationKube, "prod-east", "Browser login", "")
	assert.Contains(t, got, "Detected AI agent")
	assert.Contains(t, got, "--requester-type agent")
	assert.NotContains(t, diagnosticNextCommands(t, got), "alpacon kube login prod-east")
}

func TestWorkSessionAccountNotApproved(t *testing.T) {
	t.Setenv("AI_AGENT", "")
	reason := workSessionReasonMap[WorkSessionAccountNotApproved]
	assert.Equal(t, "the command runs as an account the session was not approved for; a session runs only as the account named when it was created", reason)

	actions := workSessionNextActions(WorkSessionAccountNotApproved, OperationCommand, "web-01", "")
	require.Len(t, actions, 2)
	assert.Equal(t, "alpacon exec -u <approved account> web-01 -- <command>", actions[0].Command)
	assert.Contains(t, actions[0].Description, "pass -u with the session's account")
	assert.Contains(t, actions[1].Command, "alpacon work-session create --scope command --server web-01")
	assert.Contains(t, actions[1].Command, "--account <account>")
	assert.NotContains(t, actions[1].Command, "--use")
	assert.Equal(t, "or create a session for this account", actions[1].Description)

	got := buildWorkSessionDiagnostic(WorkSessionAccountNotApproved, OperationCommand, "web-01", "Browser login", "")
	assert.Contains(t, got, reason)
	assert.Contains(t, got, "alpacon exec -u <approved account> web-01")

	envelope := buildWorkSessionErrorEnvelope(WorkSessionAccountNotApproved, OperationCommand, "web-01", "Browser login", "")
	assert.Equal(t, WorkSessionAccountNotApproved, envelope.ErrorCode)
	assert.Equal(t, reason, envelope.Reason)
}

func TestWorkSessionAccountNotApproved_AgentCreateIsAgentSession(t *testing.T) {
	t.Setenv("AI_AGENT", "test-agent")
	actions := workSessionNextActions(WorkSessionAccountNotApproved, OperationCommand, "web-01", "")
	require.Len(t, actions, 2)
	assert.Contains(t, actions[1].Command, "--requester-type agent")
	assert.Contains(t, actions[1].Command, "--account <account>")
}

func TestWorkSessionAccountNotApproved_RetryCarriesSession(t *testing.T) {
	t.Setenv("AI_AGENT", "")
	const bare = "alpacon exec -u <approved account> web-01 -- <command>"
	const withSession = "alpacon exec --work-session ses-1 -u <approved account> web-01 -- <command>"

	// Passed on the command line or active: the caller hands over the same ID.
	for _, tc := range []struct{ name, activeWS, want string }{
		{"session passed or active", "ses-1", withSession},
		{"neither", "", bare},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := workSessionNextActions(WorkSessionAccountNotApproved, OperationCommand, "web-01", tc.activeWS)
			require.NotEmpty(t, actions)
			assert.Equal(t, tc.want, actions[0].Command)
			envelope := buildWorkSessionErrorEnvelope(WorkSessionAccountNotApproved, OperationCommand, "web-01", "Browser login", tc.activeWS)
			assert.Equal(t, tc.want, envelope.NextActions[0].Command)
			assert.Contains(t, buildWorkSessionDiagnostic(WorkSessionAccountNotApproved, OperationCommand, "web-01", "Browser login", tc.activeWS), tc.want)
		})
	}
}
