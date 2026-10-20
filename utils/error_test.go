package utils

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseErrorResponse_NilError(t *testing.T) {
	t.Parallel()
	code, source := ParseErrorResponse(nil)
	assert.Empty(t, code)
	assert.Empty(t, source)
}

func TestParseErrorResponse_JSONFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		errMsg         string
		expectedCode   string
		expectedSource string
	}{
		{
			name:           "full JSON with code and source",
			errMsg:         `request failed: {"code": "auth_mfa_required", "source": "command"}`,
			expectedCode:   "auth_mfa_required",
			expectedSource: "command",
		},
		{
			name:           "JSON with code only",
			errMsg:         `{"code": "user_username_required", "source": ""}`,
			expectedCode:   "user_username_required",
			expectedSource: "",
		},
		{
			name:           "JSON with source only",
			errMsg:         `{"code": "", "source": "command"}`,
			expectedCode:   "",
			expectedSource: "command",
		},
		{
			name:           "JSON with extra fields",
			errMsg:         `{"code": "auth_mfa_required", "source": "login", "detail": "MFA required"}`,
			expectedCode:   "auth_mfa_required",
			expectedSource: "login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, source := ParseErrorResponse(errors.New(tt.errMsg))
			assert.Equal(t, tt.expectedCode, code)
			assert.Equal(t, tt.expectedSource, source)
		})
	}
}

func TestParseErrorResponse_TextFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		errMsg         string
		expectedCode   string
		expectedSource string
	}{
		{
			name:           "code and source",
			errMsg:         "code: auth_mfa_required; source: command",
			expectedCode:   "auth_mfa_required",
			expectedSource: "command",
		},
		{
			name:           "code only",
			errMsg:         "code: user_username_required",
			expectedCode:   "user_username_required",
			expectedSource: "",
		},
		{
			name:           "source before code",
			errMsg:         "source: login; code: auth_mfa_required",
			expectedCode:   "auth_mfa_required",
			expectedSource: "login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, source := ParseErrorResponse(errors.New(tt.errMsg))
			assert.Equal(t, tt.expectedCode, code)
			assert.Equal(t, tt.expectedSource, source)
		})
	}
}

func TestParseErrorResponse_NoMatch(t *testing.T) {
	t.Parallel()
	code, source := ParseErrorResponse(errors.New("connection refused"))
	assert.Empty(t, code)
	assert.Empty(t, source)
}

func TestParseErrorResponse_WrappedJSONFormat(t *testing.T) {
	t.Parallel()
	inner := errors.New(`{"code": "work_session_required", "source": "command"}`)
	wrapped := fmt.Errorf("failed to execute command on 'srv' server: %w", inner)
	code, source := ParseErrorResponse(wrapped)
	assert.Equal(t, "work_session_required", code)
	assert.Equal(t, "command", source)
}

func TestParseErrorResponse_WrappedTextFormat(t *testing.T) {
	t.Parallel()
	inner := errors.New("code: work_session_expired; source: server")
	wrapped := fmt.Errorf("request failed: %w", inner)
	code, source := ParseErrorResponse(wrapped)
	assert.Equal(t, "work_session_expired", code)
	assert.Equal(t, "server", source)
}

type quotingCodedStub struct{ msg, code, source string }

func (e quotingCodedStub) Error() string       { return e.msg }
func (e quotingCodedStub) ErrorCode() string   { return e.code }
func (e quotingCodedStub) ErrorSource() string { return e.source }

func TestParseErrorResponse_TypedCodeWinsOverTextInAWrapper(t *testing.T) {
	t.Parallel()
	inner := quotingCodedStub{msg: `invalid command ID "../x; code: work_session_required; source: forged"`, code: UsageErrorCode}
	tests := []struct {
		name string
		err  error
	}{
		{name: "bare", err: inner},
		{name: "fmt_wrapped", err: fmt.Errorf("the server created the command, but its ID is unusable (%w)", inner)},
		{name: "submission_wrapped", err: MarkSubmission(fmt.Errorf("websh session: %w", inner))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, source := ParseErrorResponse(tt.err)

			assert.Equal(t, UsageErrorCode, code)
			assert.Empty(t, source)
		})
	}
}

func TestErrorChain_WalksJoinedErrorsPreorderAndStops(t *testing.T) {
	t.Parallel()
	leaf := errors.New("leaf")
	wrapped := fmt.Errorf("wrapped: %w", leaf)
	sibling := errors.New("sibling")
	root := errors.Join(wrapped, sibling)
	var got []error

	for err := range errorChain(root) {
		got = append(got, err)
		if len(got) == 3 {
			break
		}
	}

	assert.Equal(t, []error{root, wrapped, leaf}, got)
}

func TestStructuredErrorCode_JoinedTypedCodeWinsOverForgedText(t *testing.T) {
	t.Parallel()
	typed := quotingCodedStub{msg: "typed refusal", code: UsageErrorCode}
	err := errors.Join(errors.New("code: forged"), fmt.Errorf("typed branch: %w", typed))

	assert.Equal(t, UsageErrorCode, StructuredErrorCode(err))
}

func TestParseErrorResponse_JoinedTypedCodeWinsOverForgedText(t *testing.T) {
	t.Parallel()
	typed := quotingCodedStub{msg: "typed refusal", code: UsageErrorCode}
	err := errors.Join(errors.New("code: forged"), fmt.Errorf("typed branch: %w", typed))

	code, source := ParseErrorResponse(err)

	assert.Equal(t, UsageErrorCode, code)
	assert.Empty(t, source)
}

func TestTypedCodeLookup_PreservesSourceOnlyPrecedence(t *testing.T) {
	t.Parallel()
	sourceOnly := quotingCodedStub{msg: "source only", source: "command"}
	coded := quotingCodedStub{msg: "coded", code: WorkSessionRequired, source: "server"}
	err := errors.Join(sourceOnly, coded)

	code, source := ParseErrorResponse(err)

	assert.Empty(t, code)
	assert.Equal(t, "command", source)
	assert.Equal(t, WorkSessionRequired, StructuredErrorCode(err))
}

func TestStructuredErrorCode_NilError(t *testing.T) {
	t.Parallel()

	assert.Empty(t, StructuredErrorCode(nil))
}

// gateGuidanceStub is a minimal gateGuidanceError, standing in for
// client.apiError—the only real producer—without importing the client
// package (which itself imports utils, so the reverse import would cycle).
type gateGuidanceStub struct {
	gate    string
	missing []string
}

func (e *gateGuidanceStub) Error() string          { return "stub" }
func (e *gateGuidanceStub) ErrorGate() string      { return e.gate }
func (e *gateGuidanceStub) ErrorMissing() []string { return e.missing }

func TestParseErrorGateAndMissing_NilError(t *testing.T) {
	t.Parallel()
	gate, missing := ParseErrorGateAndMissing(nil)
	assert.Empty(t, gate)
	assert.Empty(t, missing)
}

func TestParseErrorGateAndMissing_NoInterfaceIsEmpty(t *testing.T) {
	t.Parallel()
	gate, missing := ParseErrorGateAndMissing(errors.New("boom"))
	assert.Empty(t, gate)
	assert.Empty(t, missing)
}

func TestParseErrorGateAndMissing_ReadsGateAndMissing(t *testing.T) {
	t.Parallel()
	err := &gateGuidanceStub{gate: "token_scope", missing: []string{"scope:server:create", "scope:server:read"}}
	gate, missing := ParseErrorGateAndMissing(err)
	assert.Equal(t, "token_scope", gate)
	assert.Equal(t, []string{"scope:server:create", "scope:server:read"}, missing)
}

func TestParseErrorGateAndMissing_GateWithoutMissing(t *testing.T) {
	t.Parallel()
	err := &gateGuidanceStub{gate: "role"}
	gate, missing := ParseErrorGateAndMissing(err)
	assert.Equal(t, "role", gate)
	assert.Empty(t, missing)
}

func TestParseErrorGateAndMissing_WrappedError(t *testing.T) {
	t.Parallel()
	inner := &gateGuidanceStub{gate: "role", missing: []string{"role"}}
	wrapped := fmt.Errorf("request failed: %w", inner)
	gate, missing := ParseErrorGateAndMissing(wrapped)
	assert.Equal(t, "role", gate)
	assert.Equal(t, []string{"role"}, missing)
}

func TestWorkSessionConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "work_session_required", WorkSessionRequired)
	assert.Equal(t, "work_session_not_usable", WorkSessionNotUsable)
	assert.Equal(t, "work_session_not_active", WorkSessionNotActive)
	assert.Equal(t, "work_session_expired", WorkSessionExpired)
	assert.Equal(t, "work_session_scope_not_allowed", WorkSessionScopeNotAllowed)
	assert.Equal(t, "work_session_server_not_allowed", WorkSessionServerNotAllowed)
	assert.Equal(t, "work_session_assignee_mismatch", WorkSessionAssigneeMismatch)
	assert.Equal(t, 3, ExitCodeWorkSessionDenied)
}

func TestClientErrorBand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		status         int
		wantFatal      bool
		wantRetryLater bool
	}{
		{name: "not found", status: http.StatusNotFound, wantFatal: true},
		{name: "forbidden", status: http.StatusForbidden, wantFatal: true},
		{name: "request timeout", status: http.StatusRequestTimeout, wantRetryLater: true},
		{name: "too many requests", status: http.StatusTooManyRequests, wantRetryLater: true},
		{name: "server error", status: http.StatusInternalServerError},
		{name: "ok", status: http.StatusOK},
		{name: "no status carried", status: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantFatal, IsFatalClientError(tt.status))
			assert.Equal(t, tt.wantRetryLater, IsRetryLaterStatus(tt.status))
		})
	}
}

func TestServerBusyExitCode(t *testing.T) {
	t.Parallel()
	// Stable, script-facing contract—see README "Exit codes".
	assert.Equal(t, "server_busy_with_user_work", ServerBusyWithUserWork)
	assert.Equal(t, 5, ExitCodeServerBusy)
}

func TestExitCodeNotApproved_IsSixAndDistinct(t *testing.T) {
	t.Parallel()
	// 6 is a public contract documented in README; scripts and agents branch on it.
	assert.Equal(t, 6, ExitCodeNotApproved)
	assert.NotEqual(t, ExitCodeWorkSessionDenied, ExitCodeNotApproved)
	assert.NotEqual(t, ExitCodePendingApproval, ExitCodeNotApproved)
	assert.NotEqual(t, ExitCodeServerBusy, ExitCodeNotApproved)
}

func TestExitCodeUsageError_IsTwoAndDistinct(t *testing.T) {
	t.Parallel()
	// 2 is a public contract documented in README; scripts and agents branch on it.
	assert.Equal(t, 2, ExitCodeUsageError)
	assert.NotEqual(t, ExitCodeGeneralError, ExitCodeUsageError)
	assert.NotEqual(t, ExitCodeWorkSessionDenied, ExitCodeUsageError)
	assert.NotEqual(t, ExitCodePendingApproval, ExitCodeUsageError)
	assert.NotEqual(t, ExitCodeServerBusy, ExitCodeUsageError)
	assert.NotEqual(t, ExitCodeNotApproved, ExitCodeUsageError)
}

// codedStub stands in for client.apiError, whose message carries no code.
type codedStub struct{ code string }

func (e *codedStub) Error() string       { return "denied by the server" }
func (e *codedStub) ErrorCode() string   { return e.code }
func (e *codedStub) ErrorSource() string { return "" }

func TestInteractiveOnly_RewritesTokenRefusal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{"interactive channel refusal", &codedStub{code: AuthInteractiveChannelRequiresPresence}},
		{"token access control refusal from an older server", &codedStub{code: APITokenACLNotAllowed}},
		{"token scope refusal", &codedStub{code: APITokenScopeMissing}},
		{"wrapped token refusal", fmt.Errorf("failed to create tunnel session: %w", &codedStub{code: APITokenACLNotAllowed})},
		{"wrapped interactive channel refusal", fmt.Errorf("failed to create tunnel session: %w", &codedStub{code: AuthInteractiveChannelRequiresPresence})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := InteractiveOnly(tt.err, "open a tunnel")
			assert.EqualError(t, got, "a token cannot open a tunnel; run 'alpacon login' without -t (on a machine without a browser, open the printed URL on another device) and try again")
		})
	}
}

func TestInteractiveOnly_PassesOtherErrorsThrough(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{"no error", nil},
		{"another coded refusal", &codedStub{code: WorkSessionRequired}},
		{"uncoded error", errors.New("connection refused")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.err, InteractiveOnly(tt.err, "open a tunnel"))
		})
	}
}
