package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// Serial: each case swaps os.Stderr through CaptureOutput.
func TestReportLogout(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStderr string
	}{
		{name: "config removed", wantStderr: "Logout succeeded!"},
		{name: "another shell logged in meanwhile", err: fmt.Errorf("delete: %w", config.ErrLoginChanged),
			wantStderr: "Logged out, but kept the config: another shell logged in since this command started."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr := testutil.CaptureOutput(t, func() {
				reportLogout(tt.err)
			})

			assert.Contains(t, stderr, tt.wantStderr)
		})
	}
}

// blacklistRefusal is a coded refusal of the token blacklist call.
type blacklistRefusal struct{ hint string }

func (e blacklistRefusal) Error() string {
	return "permission denied (HTTP 403, code: permission_denied)"
}
func (e blacklistRefusal) ErrorCode() string   { return "permission_denied" }
func (e blacklistRefusal) ErrorSource() string { return "" }
func (e blacklistRefusal) ErrorHint() string   { return e.hint }

func TestBlacklistFailure(t *testing.T) {
	t.Parallel()
	const hint = "This account or credential does not have permission for this action."

	hinted := blacklistFailure(blacklistRefusal{hint: hint})
	assert.Equal(t, "Failed to set black list: permission denied (HTTP 403, code: permission_denied).", hinted.Error())
	assert.Equal(t, hint, utils.RefusalHint(hinted))

	// Without a hint the message is today's, retry advice included.
	plain := blacklistFailure(errors.New("request failed"))
	assert.Equal(t, "Failed to set black list. Please try again later.", plain.Error())
	assert.Empty(t, utils.RefusalHint(plain))
}
