package agent

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestAgentRestart(t *testing.T) {
	t.Parallel()
	const actionRoute = "POST /api/servers/servers/550e8400-e29b-41d4-a716-446655440000/actions/"
	tests := []struct {
		name        string
		args        []string
		status      int
		body        string
		wantCode    int
		want        string
		wantActions int
	}{
		{name: "requests the restart", args: []string{"my-server", "-y"}, status: http.StatusCreated, body: `{}`, wantActions: 1},
		{name: "busy server", args: []string{"my-server", "-y"}, status: http.StatusConflict, body: `{"code": "server_busy_with_user_work"}`, wantCode: utils.ExitCodeServerBusy, want: "busy with active user work", wantActions: 1},
		{name: "server error", args: []string{"my-server", "-y", "--force"}, status: http.StatusInternalServerError, body: `{"detail": "unavailable"}`, wantCode: utils.ExitCodeGeneralError, want: "Failed to restart the agent", wantActions: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/servers/servers/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-server"}]}`},
				actionRoute:                 {Status: tt.status, Body: tt.body},
			})

			failure, code := cmdtest.Run(newRestartAgentCmd(ts.Factory(), utils.Stdio), tt.args...)

			assert.Equal(t, tt.wantCode, code, failure)
			assert.Contains(t, failure, tt.want)
			assert.Equal(t, tt.wantActions, ts.Hits(actionRoute))
		})
	}
}

func TestAgentRestartFailsWithoutALogin(t *testing.T) {
	t.Parallel()

	failure, code := cmdtest.Run(newRestartAgentCmd(cmdtest.NoClient, utils.Stdio), "my-server", "-y")

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, failure, "Connection to Alpacon API failed: not logged in")
}
