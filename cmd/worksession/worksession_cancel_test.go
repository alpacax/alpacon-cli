package worksession

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestWorkSessionCancel(t *testing.T) {
	t.Parallel()
	const cancelRoute = "POST /api/work-sessions/sessions/550e8400-e29b-41d4-a716-446655440000/cancel/"
	tests := []struct {
		name        string
		arg         string
		status      int
		wantCode    int
		wantFailure string
		wantCancels int
	}{
		{name: "cancels a pending session", arg: "550e8400-e29b-41d4-a716-446655440000", status: http.StatusOK, wantCancels: 1},
		{name: "server refuses", arg: "550e8400-e29b-41d4-a716-446655440000", status: http.StatusConflict, wantCode: utils.ExitCodeGeneralError, wantFailure: "Failed to cancel work session", wantCancels: 1},
		{name: "id that is not one path segment", arg: "../etc", wantCode: utils.ExitCodeUsageError, wantFailure: "Failed to cancel work session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				cancelRoute: {Status: tt.status, Body: `{"code": "invalid_state", "detail": "the session is not pending"}`},
			})

			failure, code := cmdtest.Run(newWorkSessionCancelCmd(ts.Factory(), utils.Stdio), tt.arg)

			assert.Equal(t, tt.wantCode, code, failure)
			assert.Contains(t, failure, tt.wantFailure)
			assert.Equal(t, tt.wantCancels, ts.Hits(cancelRoute))
		})
	}
}

func TestWorkSessionCancelFailsWithoutALogin(t *testing.T) {
	t.Parallel()

	failure, code := cmdtest.Run(newWorkSessionCancelCmd(cmdtest.NoClient, utils.Stdio), "550e8400-e29b-41d4-a716-446655440000")

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, failure, "Connection to Alpacon API failed: not logged in")
}
