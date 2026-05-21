package utils

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseErrorResponse_NilError(t *testing.T) {
	code, source := ParseErrorResponse(nil)
	assert.Equal(t, "", code)
	assert.Equal(t, "", source)
}

func TestParseErrorResponse_JSONFormat(t *testing.T) {
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
	code, source := ParseErrorResponse(errors.New("connection refused"))
	assert.Equal(t, "", code)
	assert.Equal(t, "", source)
}

func TestParseErrorResponse_WrappedJSONFormat(t *testing.T) {
	inner := errors.New(`{"code": "work_session_required", "source": "command"}`)
	wrapped := fmt.Errorf("failed to execute command on 'srv' server: %w", inner)
	code, source := ParseErrorResponse(wrapped)
	assert.Equal(t, "work_session_required", code)
	assert.Equal(t, "command", source)
}

func TestParseErrorResponse_WrappedTextFormat(t *testing.T) {
	inner := errors.New("code: work_session_expired; source: server")
	wrapped := fmt.Errorf("request failed: %w", inner)
	code, source := ParseErrorResponse(wrapped)
	assert.Equal(t, "work_session_expired", code)
	assert.Equal(t, "server", source)
}

func TestWorkSessionConstants(t *testing.T) {
	assert.Equal(t, "work_session_required", WorkSessionRequired)
	assert.Equal(t, "work_session_not_usable", WorkSessionNotUsable)
	assert.Equal(t, "work_session_not_active", WorkSessionNotActive)
	assert.Equal(t, "work_session_expired", WorkSessionExpired)
	assert.Equal(t, "work_session_scope_not_allowed", WorkSessionScopeNotAllowed)
	assert.Equal(t, "work_session_server_not_allowed", WorkSessionServerNotAllowed)
	assert.Equal(t, "work_session_assignee_mismatch", WorkSessionAssigneeMismatch)
	assert.Equal(t, 3, ExitCodeWorkSessionDenied)
}
