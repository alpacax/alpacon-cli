package username

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// Serial: it captures stdout and sets utils.OutputFormat.
func TestUsernameGetPrintsTheUsername(t *testing.T) {
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/iam/users/-/": {Status: http.StatusOK, Body: `{"username": "alice"}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(t, newUsernameGetCmd(ts.Factory()))

	assert.Equal(t, 0, code, stderr)
	assert.JSONEq(t, `{"username": "alice"}`, stdout)
}

func TestUsernameGetFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "username not set", status: http.StatusOK, body: `{"username": ""}`, want: "Username is not set. Run 'alpacon username set <name>' to set it."},
		{name: "server error", status: http.StatusInternalServerError, body: `{"detail": "unavailable"}`, want: "Failed to retrieve the current user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/iam/users/-/": {Status: tt.status, Body: tt.body},
			})

			failure, code := cmdtest.Run(newUsernameGetCmd(ts.Factory()))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
