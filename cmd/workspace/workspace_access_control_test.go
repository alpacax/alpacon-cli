package workspace

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// Serial: it captures stdout and sets utils.OutputFormat.
func TestWorkspaceAccessControlPrintsTheServerAnswer(t *testing.T) {
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/workspaces/access-control/-/": {Status: http.StatusOK, Body: `{"allow_sudo": true, "allow_tunneling": false}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(t, newWorkspaceAccessControlCmd(ts.Factory()))

	assert.Equal(t, 0, code, stderr)
	assert.JSONEq(t, `{"allow_sudo": true, "allow_tunneling": false}`, stdout)
}

func TestWorkspaceAccessControlFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", status: http.StatusInternalServerError, want: "Failed to retrieve access control settings"},
		{name: "forbidden", status: http.StatusForbidden, want: "Failed to retrieve access control settings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/workspaces/access-control/-/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newWorkspaceAccessControlCmd(newClient))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
