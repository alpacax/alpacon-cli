package iam

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUsernameErrorMessage(t *testing.T) {
	tests := []struct {
		code      string
		wantOK    bool
		wantMatch string
	}{
		{"user_username_invalid", true, "lowercase letters"},
		{"user_username_disallowed", true, "reserved"},
		{"user_username_in_use", true, "already in use"},
		{"user_username_already_set", true, "already set"},
		{"approval_superuser_approve_required", true, "superuser approval"},
		{"some_unknown_code", false, ""},
		{"", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			msg, ok := UsernameErrorMessage(tt.code)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Contains(t, msg, tt.wantMatch)
			} else {
				assert.Empty(t, msg)
			}
		})
	}
}

func TestIsRetryableUsernameError(t *testing.T) {
	assert.True(t, isRetryableUsernameError("user_username_invalid"))
	assert.True(t, isRetryableUsernameError("user_username_disallowed"))
	assert.True(t, isRetryableUsernameError("user_username_in_use"))
	assert.False(t, isRetryableUsernameError("user_username_already_set"))
	assert.False(t, isRetryableUsernameError("approval_superuser_approve_required"))
	assert.False(t, isRetryableUsernameError("unknown"))
}
