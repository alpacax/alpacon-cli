package token

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestTokenDeleteDeletesTheNamedToken(t *testing.T) {
	t.Parallel()
	const deleteRoute = "DELETE /api/auth/tokens/550e8400-e29b-41d4-a716-446655440000/"
	tests := []struct {
		name        string
		arg         string
		wantLookups int
	}{
		{name: "by name", arg: "my-api-token", wantLookups: 1},
		{name: "by id", arg: "550e8400-e29b-41d4-a716-446655440000", wantLookups: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/auth/tokens/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-api-token"}]}`},
				deleteRoute:             {Status: http.StatusNoContent},
			})

			failure, code := cmdtest.Run(newTokenDeleteCmd(ts.Factory()), tt.arg, "-y")

			assert.Equal(t, 0, code, failure)
			assert.Equal(t, 1, ts.Hits(deleteRoute))
			assert.Equal(t, tt.wantLookups, ts.Hits("GET /api/auth/tokens/"))
		})
	}
}

func TestTokenDeleteFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		tokens string
		status int
		want   string
	}{
		{name: "unknown token", tokens: `{"count": 0, "results": []}`, status: http.StatusNoContent, want: "Failed to delete the api token: no token found with the given name"},
		{name: "server refuses", tokens: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-api-token"}]}`, status: http.StatusForbidden, want: "Failed to delete the api token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/auth/tokens/": {Status: http.StatusOK, Body: tt.tokens},
				"DELETE /api/auth/tokens/550e8400-e29b-41d4-a716-446655440000/": {Status: tt.status, Body: `{"code": "permission_denied", "detail": "denied"}`},
			})

			failure, code := cmdtest.Run(newTokenDeleteCmd(ts.Factory()), "my-api-token", "-y")

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
