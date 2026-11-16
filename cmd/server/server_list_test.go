package server

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// Serial: it captures stdout and sets utils.OutputFormat.
func TestServerListPrintsWhatTheServerReturned(t *testing.T) {
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/servers/servers/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-server", "remote_ip": "10.0.0.5", "is_connected": true, "os_name": "Ubuntu", "os_version": "24.04"}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(t, newServerListCmd(ts.Factory()))

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"my-server"`)
	assert.Contains(t, stdout, `"10.0.0.5"`)
	assert.Contains(t, stdout, `"Ubuntu 24.04"`)
}

func TestServerListFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", status: http.StatusInternalServerError, want: "Failed to retrieve the servers"},
		{name: "forbidden", status: http.StatusForbidden, want: "Failed to retrieve the servers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/servers/servers/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newServerListCmd(newClient))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
