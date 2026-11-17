package log

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestLogPrintsTheServersLogs(t *testing.T) {
	t.Parallel()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/servers/servers/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-server"}]}`},
		"GET /api/history/logs/":    {Status: http.StatusOK, Body: `{"results": [{"id": 7, "program": "alpamon", "level": 20, "msg": "connected", "date": "2026-11-01T09:00:00Z"}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command { return NewLogCmd(ts.Factory(), ios) }, "my-server", "--tail", "1")

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"alpamon"`)
	assert.Equal(t, 1, ts.Hits("GET /api/history/logs/"))
}

func TestLogFails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		servers  string
		wantCode int
		want     string
	}{
		{name: "tail zero", args: []string{"my-server", "--tail", "0"}, wantCode: utils.ExitCodeUsageError, want: "--tail must be a positive integer."},
		{name: "unknown server", args: []string{"my-server"}, servers: `{"count": 0, "results": []}`, wantCode: utils.ExitCodeGeneralError, want: "Failed to get logs"},
		{name: "no server argument", wantCode: utils.ExitCodeUsageError, want: "accepts 1 arg(s), received 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/servers/servers/": {Status: http.StatusOK, Body: tt.servers},
			})

			failure, code := cmdtest.Run(NewLogCmd(ts.Factory(), utils.Stdio), tt.args...)

			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, failure, tt.want)
			assert.Zero(t, ts.Hits("GET /api/history/logs/"))
		})
	}
}
