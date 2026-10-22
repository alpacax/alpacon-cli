package cmd

import (
	"fmt"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
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
