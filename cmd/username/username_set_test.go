package username

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestUsernameSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode int
		want     string
	}{
		{name: "sets the username", status: http.StatusOK, body: `{"username": "alice"}`},
		{name: "name in use", status: http.StatusBadRequest, body: `{"code": "user_username_in_use"}`, wantCode: utils.ExitCodeGeneralError, want: "already in use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"POST /api/iam/username/": {Status: tt.status, Body: tt.body},
			})

			failure, code := cmdtest.Run(newUsernameSetCmd(ts.Factory(), utils.Stdio), "alice")

			assert.Equal(t, tt.wantCode, code, failure)
			assert.Contains(t, failure, tt.want)
			assert.Equal(t, 1, ts.Hits("POST /api/iam/username/"))
		})
	}
}
