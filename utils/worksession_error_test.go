package utils

import (
	"encoding/json"
	"errors"
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
		code       string
		wantReason string
		wantNext   string
	}{
		{WorkSessionRequired, "no WorkSession selected", "worksession list"},
		{WorkSessionNotActive, "not yet active", "worksession current"},
		{WorkSessionExpired, "has expired", "worksession extend"},
		{WorkSessionScopeNotAllowed, "does not include this scope", "worksession create"},
		{WorkSessionServerNotAllowed, "target server is not in this session", "worksession create"},
		{WorkSessionAssigneeMismatch, "assigned to another principal", "worksession use"},
		{WorkSessionNotUsable, "no longer usable", "worksession create"},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := buildWorkSessionDiagnostic(tt.code, "websh", "prod-1", "Browser login", "")
			assert.Contains(t, got, tt.wantReason)
			assert.Contains(t, got, tt.wantNext)
			assert.Contains(t, got, "required scope")
			assert.Contains(t, got, "prod-1")
			assert.Contains(t, got, "Browser login (interactive)")
			assert.Contains(t, got, "Note:")
		})
	}
}

func TestBuildWorkSessionDiagnostic_APIToken(t *testing.T) {
	got := buildWorkSessionDiagnostic(WorkSessionRequired, "command", "srv-1", "API token", "")
	assert.Contains(t, got, "API token")
	assert.NotContains(t, got, "(interactive)")
}

func TestBuildWorkSessionJSON(t *testing.T) {
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
			got := buildWorkSessionJSON(code, "command", "srv-1", "API token", "")

			var envelope workSessionErrorJSON
			assert.NoError(t, json.Unmarshal([]byte(got), &envelope))
			assert.False(t, envelope.OK)
			assert.Equal(t, code, envelope.ErrorCode)
			assert.Equal(t, "command", envelope.Command)
			assert.Equal(t, "command", envelope.Context.RequiredScope)
			assert.Equal(t, []string{"srv-1"}, envelope.Context.TargetServers)
			assert.Nil(t, envelope.Context.CurrentWorksession)
			assert.NotEmpty(t, envelope.NextActions)
		})
	}
}

func TestBuildWorkSessionJSON_WithActiveWS(t *testing.T) {
	got := buildWorkSessionJSON(WorkSessionExpired, "webftp", "srv-2", "Browser login", "abc-123-uuid")

	var envelope workSessionErrorJSON
	assert.NoError(t, json.Unmarshal([]byte(got), &envelope))
	assert.NotNil(t, envelope.Context.CurrentWorksession)
	assert.Equal(t, "abc-123-uuid", *envelope.Context.CurrentWorksession)
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
