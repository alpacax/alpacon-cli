package workspace

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// Serial: it captures stdout and sets utils.OutputFormat.
func TestWorkspacePreferencesPrintsTheServerAnswer(t *testing.T) {
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/workspaces/preferences/-/": {Status: http.StatusOK, Body: `{"timezone": "Asia/Seoul", "language": "en"}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(t, newWorkspacePreferencesCmd(ts.Factory()))

	assert.Equal(t, 0, code, stderr)
	assert.JSONEq(t, `{"timezone": "Asia/Seoul", "language": "en"}`, stdout)
}

func TestWorkspacePreferencesFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", status: http.StatusInternalServerError, want: "Failed to retrieve workspace preferences"},
		{name: "forbidden", status: http.StatusForbidden, want: "Failed to retrieve workspace preferences"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/workspaces/preferences/-/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newWorkspacePreferencesCmd(newClient))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
