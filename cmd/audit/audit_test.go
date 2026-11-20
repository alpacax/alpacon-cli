package audit

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestAuditPrintsTheActivity(t *testing.T) {
	t.Parallel()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/audit/activity/": {Status: http.StatusOK, Body: `{"results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "username": "alice", "ip": "10.0.0.7", "app": "servers", "action": "update", "status_code": 200, "model": "server", "added_at": "2026-11-01T09:00:00Z"}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command { return NewAuditCmd(ts.Factory(), ios) }, "--tail", "1")

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"alice"`)
	assert.Contains(t, stdout, `"10.0.0.7"`)
}

func TestAuditFails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		wantCode int
		want     string
		wantHits int
	}{
		{name: "tail zero", args: []string{"--tail", "0"}, wantCode: utils.ExitCodeUsageError, want: "--tail must be a positive integer."},
		{name: "server error", wantCode: utils.ExitCodeGeneralError, want: "Failed to get audit logs", wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/audit/activity/": {Status: http.StatusInternalServerError, Body: `{"detail": "unavailable"}`},
			})

			failure, code := cmdtest.Run(NewAuditCmd(ts.Factory(), utils.Stdio), tt.args...)

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, failure, tt.want)
			assert.Equal(t, tt.wantHits, ts.Hits("GET /api/audit/activity/"))
		})
	}
}
