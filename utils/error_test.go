package utils

import (
	"errors"
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
