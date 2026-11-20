package iam

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestGroupListPrintsWhatTheServerReturned(t *testing.T) {
	t.Parallel()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/iam/groups/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"name": "my-group", "display_name": "My group", "gid": 3001, "num_members": 2}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command { return newGroupListCmd(ts.Factory(), ios) })

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"my-group"`)
	assert.Contains(t, stdout, `"My group"`)
	assert.Contains(t, stdout, `3001`)
}

func TestGroupListFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", status: http.StatusInternalServerError, want: "Failed to retrieve the group list"},
		{name: "forbidden", status: http.StatusForbidden, want: "Failed to retrieve the group list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/iam/groups/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newGroupListCmd(newClient, utils.Stdio))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
