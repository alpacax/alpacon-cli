package utils

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsWorkSessionCode(t *testing.T) {
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
		{WorkSessionAssigneeMismatch, "assigned to another principal", "work-session use", false, false},
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
	got := buildWorkSessionDiagnostic(WorkSessionRequired, "command", "srv-1", "API token", "")
	assert.Contains(t, got, "API token")
	assert.NotContains(t, got, "(interactive)")
}

func TestBuildWorkSessionErrorEnvelope(t *testing.T) {
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
	envelope := buildWorkSessionErrorEnvelope(WorkSessionExpired, "webftp", "srv-2", "Browser login", "abc-123-uuid")

	assert.NotNil(t, envelope.Context.CurrentWorksession)
	assert.Equal(t, "abc-123-uuid", *envelope.Context.CurrentWorksession)
	// Expired case: <ID> placeholder must be substituted with the known active UUID.
	assert.Contains(t, envelope.NextActions, "alpacon work-session extend abc-123-uuid")
	for _, action := range envelope.NextActions {
		assert.NotContains(t, action, "<ID>")
	}
}

func TestBuildWorkSessionErrorEnvelope_ExpiredWithoutActiveWS(t *testing.T) {
	// When activeWS is unknown, the placeholder must remain.
	envelope := buildWorkSessionErrorEnvelope(WorkSessionExpired, "webftp", "srv-2", "Browser login", "")

	assert.Contains(t, envelope.NextActions, "alpacon work-session extend <ID>")
}

func TestBuildWorkSessionErrorEnvelope_RequiredKeepsPlaceholder(t *testing.T) {
	// For work_session_required, activeWS is unrelated to the suggested `use <ID>`,
	// so the placeholder must NOT be substituted even when activeWS is known.
	envelope := buildWorkSessionErrorEnvelope(WorkSessionRequired, "command", "srv-1", "Browser login", "abc-123-uuid")

	// The use action carries an inline comment, so match as a substring.
	assert.Contains(t, strings.Join(envelope.NextActions, "\n"), "alpacon work-session use <ID>")
}

func TestHandleWorkSessionError_NoOp(t *testing.T) {
	// Non-WorkSession errors must not trigger any exit — just return.
	// If HandleWorkSessionError calls os.Exit for this error, the test process dies.
	err := errors.New("some unrelated error")
	HandleWorkSessionError(err, "websh", "srv", "Browser login", "")
	// reaching here means no exit — test passes
}

func TestHandleWorkSessionError_NilError(t *testing.T) {
	HandleWorkSessionError(nil, "websh", "srv", "Browser login", "")
}
