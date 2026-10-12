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
