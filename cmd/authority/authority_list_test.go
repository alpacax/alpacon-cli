package authority

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// Serial: it captures stdout and sets utils.OutputFormat.
func TestAuthorityListPrintsWhatTheServerReturned(t *testing.T) {
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/cert/authorities/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-authority", "organization": "Example", "domain": "example.com", "agent": {"name": "my-server"}, "signed_at": "2026-11-01T09:00:00Z"}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(t, newAuthorityListCmd(ts.Factory()))

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"my-authority"`)
	assert.Contains(t, stdout, `"example.com"`)
	assert.Contains(t, stdout, `"my-server"`)
}

func TestAuthorityListFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", status: http.StatusInternalServerError, want: "Failed to retrieve the authority list"},
		{name: "forbidden", status: http.StatusForbidden, want: "Failed to retrieve the authority list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/cert/authorities/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newAuthorityListCmd(newClient))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
