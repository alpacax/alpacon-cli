package iam

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestGroupDeleteDeletesOnlyWhenAllowed(t *testing.T) {
	t.Parallel()
	const deleteRoute = "DELETE /api/iam/groups/550e8400-e29b-41d4-a716-446655440000/"
	tests := []struct {
		name        string
		currentUser string
		groups      string
		wantCode    int
		wantFailure string
		wantDeletes int
	}{
		{
			name:        "staff deletes the group",
			currentUser: `{"username": "alice", "is_staff": true}`,
			groups:      `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "developers"}]}`,
			wantDeletes: 1,
		},
		{
			name:        "general user is refused",
			currentUser: `{"username": "bob"}`,
			groups:      `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "developers"}]}`,
			wantCode:    utils.ExitCodeGeneralError,
			wantFailure: "You do not have the permission to delete groups.",
		},
		{
			name:        "unknown group",
			currentUser: `{"username": "alice", "is_superuser": true}`,
			groups:      `{"count": 0, "results": []}`,
			wantCode:    utils.ExitCodeGeneralError,
			wantFailure: "Failed to delete the group: no group found with the given name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/iam/users/-/": {Status: http.StatusOK, Body: tt.currentUser},
				"GET /api/iam/groups/":  {Status: http.StatusOK, Body: tt.groups},
				deleteRoute:             {Status: http.StatusNoContent},
			})

			failure, code := cmdtest.Run(newGroupDeleteCmd(ts.Factory()), "developers", "-y")

			assert.Equal(t, tt.wantCode, code, failure)
			if tt.wantFailure != "" {
				assert.Contains(t, failure, tt.wantFailure)
			}
			assert.Equal(t, tt.wantDeletes, ts.Hits(deleteRoute))
		})
	}
}

func TestGroupDeleteFailsWithoutALogin(t *testing.T) {
	t.Parallel()

	failure, code := cmdtest.Run(newGroupDeleteCmd(cmdtest.NoClient), "developers", "-y")

	assert.Equal(t, utils.ExitCodeGeneralError, code)
	assert.Contains(t, failure, "Connection to Alpacon API failed: not logged in")
}
