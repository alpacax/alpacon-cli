package webftp

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

const logRoute = "GET /api/history/webftp-logs/"

func TestWebFTPLogPrintsTheTransfers(t *testing.T) {
	t.Parallel()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		logRoute: {Status: http.StatusOK, Body: `{"results": [{"added_at": "2026-11-01T09:00:00Z", "file_name": "backup.tar.gz", "action": "upload", "size": 1024, "success": true, "user": {"name": "alice"}, "server": {"name": "my-server"}}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command { return newWebFTPCmd(ts.Factory(), ios) }, "--tail", "1")

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"backup.tar.gz"`)
	assert.Contains(t, stdout, `"my-server"`)
}

func TestWebFTPLogFails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		wantCode int
		want     string
		wantHits int
	}{
		{name: "tail zero", args: []string{"--tail", "0"}, wantCode: utils.ExitCodeUsageError, want: "--tail must be a positive integer."},
		{name: "unknown server", args: []string{"--server", "my-server"}, wantCode: utils.ExitCodeGeneralError, want: `Failed to get WebFTP logs: --server "my-server"`},
		{name: "server error", wantCode: utils.ExitCodeGeneralError, want: "Failed to get WebFTP logs", wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/servers/servers/": {Status: http.StatusOK, Body: `{"count": 0, "results": []}`},
				logRoute:                    {Status: http.StatusInternalServerError, Body: `{"detail": "unavailable"}`},
			})

			failure, code := cmdtest.Run(newWebFTPCmd(ts.Factory(), utils.Stdio), tt.args...)

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, failure, tt.want)
			assert.Equal(t, tt.wantHits, ts.Hits(logRoute))
		})
	}
}
