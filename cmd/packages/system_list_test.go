package packages

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// Serial: it captures stdout and sets utils.OutputFormat.
func TestSystemPackageListPrintsEveryPackage(t *testing.T) {
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/packages/system/entries/": {Status: http.StatusOK, Body: `{"count": 1, "results": [
			{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "osquery", "version": "5.8.2",
			 "arch": "amd64", "platform": "debian", "owner": {"name": "alice"}}]}`},
	})

	stdout, _, code := cmdtest.RunJSON(t, newSystemPackageListCmd(ts.Factory()))

	assert.Equal(t, 0, code)
	assert.JSONEq(t, `[{"name": "osquery", "version": "5.8.2", "arch": "amd64", "platform": "debian", "owner": "alice"}]`, stdout)
}

func TestSystemPackageListFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", status: http.StatusInternalServerError, want: "Failed to retrieve the system packages"},
		{name: "forbidden", status: http.StatusForbidden, want: "Failed to retrieve the system packages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/packages/system/entries/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newSystemPackageListCmd(newClient))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
