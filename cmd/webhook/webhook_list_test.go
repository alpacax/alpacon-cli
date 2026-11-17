package webhook

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestWebhookListPrintsWhatTheServerReturned(t *testing.T) {
	t.Parallel()
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/notifications/webhooks/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-webhook", "url": "https://hooks.example.com/alpacon", "provider": "slack", "ssl_verify": true, "enabled": true}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(func(ios *utils.IOStreams) *cobra.Command { return newWebhookListCmd(ts.Factory(), ios) })

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"my-webhook"`)
	assert.Contains(t, stdout, `"https://hooks.example.com/alpacon"`)
	assert.Contains(t, stdout, `"slack"`)
}

func TestWebhookListFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", status: http.StatusInternalServerError, want: "Failed to get webhooks"},
		{name: "forbidden", status: http.StatusForbidden, want: "Failed to get webhooks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/notifications/webhooks/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newWebhookListCmd(newClient, utils.Stdio))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
