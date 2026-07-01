package username

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetUsernameErrorText(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantMatch  string
		wantDetail string // when set, the fallback must preserve the original error detail
	}{
		{"in_use mapped", errors.New(`{"code": "user_username_in_use", "source": ""}`), "already in use", ""},
		{"disallowed mapped", errors.New(`{"code": "user_username_disallowed"}`), "reserved", ""},
		{"invalid mapped", errors.New(`{"code": "user_username_invalid"}`), "lowercase letters", ""},
		{"unknown falls back", errors.New("some network failure"), "Failed to set username", "some network failure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setUsernameErrorText(tt.err)
			assert.Contains(t, got, tt.wantMatch)
			if tt.wantDetail != "" {
				assert.Contains(t, got, tt.wantDetail)
			}
		})
	}
}
