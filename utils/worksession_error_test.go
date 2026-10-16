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
		{"interactive Websh needs a login without a token", OperationWebsh, "cannot open an interactive terminal; it needs 'alpacon login' without -t", "bypass this check"},
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
		ls      = "alpacon work-session ls --status active"
		use     = "alpacon work-session use <ID>"
		current = "alpacon work-session current"
		create  = `alpacon work-session create --scope command --server <SERVER> --expires-in 1h --purpose "<intent>" --use`
	)
	reuseOrCreate := []string{ls, use, create}
	return []struct {
		code     string
		activeWS string
		want     []string
	}{
		{WorkSessionRequired, "", reuseOrCreate},
		{WorkSessionNotUsable, "", reuseOrCreate},
		{WorkSessionAssigneeMismatch, "", reuseOrCreate},
		{WorkSessionNotActive, "", []string{current, ls, use, create}},
		{WorkSessionExpired, "", []string{"alpacon work-session extend <ID>", create}},
		{WorkSessionExpired, "abc-123-uuid", []string{"alpacon work-session extend abc-123-uuid", create}},
		{WorkSessionScopeNotAllowed, "", reuseOrCreate},
		{WorkSessionServerNotAllowed, "", reuseOrCreate},
		{WorkSessionAccountNotApproved, "", reuseOrCreate},
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
	for _, line := range strings.Split(block, "\n") {
		cmd, _, _ := strings.Cut(strings.TrimPrefix(line, "  "), "  # ")
		cmds = append(cmds, cmd)
	}
	return cmds
}

func TestBuildWorkSessionDiagnostic_KubeIgnoresServerName(t *testing.T) {
	t.Parallel()
	// A kube session names no server, so the create suggestion keeps its placeholder.
	got := buildWorkSessionDiagnostic(WorkSessionRequired, OperationKube, "prod-1", "Browser login", "")
	assert.Contains(t, got, "  target server : prod-1\n")
	assert.Equal(t, []string{
		"alpacon work-session ls --status active",
		"alpacon work-session use <ID>",
		`alpacon work-session create --scope command --server <SERVER> --expires-in 1h --purpose "<intent>" --use`,
	}, diagnosticNextCommands(t, got))
}

func TestKubeWorkSessionCasesCoverEveryCode(t *testing.T) {
	t.Parallel()
	// The agent-session code has its own test below: its next step is a note, not a command.
	covered := map[string]bool{WorkSessionAgentSessionRequired: true}
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
			got := buildWorkSessionDiagnostic(tc.code, OperationKube, "", "Browser login", tc.activeWS)

			lines := strings.Split(got, "\n")
			require.NotEmpty(t, lines)
			assert.Equal(t, Red("Error")+": opening a kube session requires an active WorkSession on this authentication.", lines[0])
			assert.Contains(t, got, workSessionReasonMap[tc.code])
			assert.Contains(t, got, "Browser login (interactive)")
			assert.NotContains(t, got, "required scope")
			assert.NotContains(t, got, "  target server : ")
			assert.NotContains(t, got, "--scope kube")
			assert.NotContains(t, got, "AI agent")
			assert.NotContains(t, got, "--requester-type")
			assert.NotContains(t, got, "the kube operation")
			assert.Equal(t, tc.want, diagnosticNextCommands(t, got))
			assert.Equal(t, "Note: Tokens issued by Alpacon (service or personal API token) cannot open a kube session; it needs 'alpacon login' without -t.", lines[len(lines)-1])
		})
	}
}

func TestBuildWorkSessionErrorEnvelope_Kube(t *testing.T) {
	t.Parallel()
	for _, tc := range kubeWorkSessionCases() {
		t.Run(kubeCaseName(tc.code, tc.activeWS), func(t *testing.T) {
			t.Parallel()
			envelope := buildWorkSessionErrorEnvelope(tc.code, OperationKube, "", "Browser login", tc.activeWS)

			assert.False(t, envelope.OK)
			assert.Equal(t, ExitCodeWorkSessionDenied, envelope.ExitCode)
			assert.Equal(t, tc.code, envelope.ErrorCode)
			assert.Equal(t, "opening a kube session requires an active WorkSession on this authentication.", envelope.Message)
			assert.Equal(t, workSessionReasonMap[tc.code], envelope.Reason)
			assert.Empty(t, envelope.Context.RequiredScope)
			assert.Equal(t, []string{}, envelope.Context.TargetServers)
			if tc.activeWS == "" {
				assert.Nil(t, envelope.Context.CurrentWorksession)
			} else if assert.NotNil(t, envelope.Context.CurrentWorksession) {
				assert.Equal(t, tc.activeWS, *envelope.Context.CurrentWorksession)
			}
			assert.Equal(t, tc.want, nextActionCommands(envelope.NextActions))
			for _, action := range envelope.NextActions {
				assert.NotContains(t, action.Command, "--scope kube")
				assert.NotContains(t, action.Command, "--requester-type")
				assert.NotContains(t, action.Description, "AI agent")
			}
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

func TestBuildWorkSessionErrorEnvelope_KubeKeepsRequiredScopeField(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(buildWorkSessionErrorEnvelope(WorkSessionRequired, OperationKube, "", "Browser login", ""))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"required_scope":""`)
}

func TestKubeWorkSessionNextActions_CreateDescription(t *testing.T) {
	t.Parallel()
	actions := workSessionNextActions(WorkSessionRequired, OperationKube, "", "")
	require.Len(t, actions, 3)
	assert.Equal(t, "find an existing active session you own", actions[0].Description)
	assert.Equal(t, "attach it", actions[1].Description)
	assert.Equal(t, "none active? create one; any active session you own works for kube", actions[2].Description)
}

// Under a detected agent a kube session cannot be opened at all: an agent
// session is refused and a user session is refused again. The only way out is
// a person's own terminal, so no use or --use is suggested.
func TestKubeWorkSessionAgentSessionRequired(t *testing.T) {
	t.Setenv("AI_AGENT", "test-agent")
	want := "kube needs a person's own terminal: unset the AI agent variables or run the command from your own terminal, then attach a session you own"

	actions := workSessionNextActions(WorkSessionAgentSessionRequired, OperationKube, "", "")
	require.Len(t, actions, 1)
	assert.Empty(t, actions[0].Command)
	assert.Equal(t, want, actions[0].Description)

	got := buildWorkSessionDiagnostic(WorkSessionAgentSessionRequired, OperationKube, "", "Browser login", "")
	assert.Contains(t, got, "Detected AI agent")
	assert.Contains(t, got, "Unset those variables, or run the command from your own terminal.")
	assert.Contains(t, got, "  "+want+"\n")
	assert.NotContains(t, got, "work-session use")
	assert.NotContains(t, got, "--use")
	assert.NotContains(t, got, "work-session create")
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
