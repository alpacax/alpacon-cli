package server

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestServerDelete(t *testing.T) {
	t.Parallel()
	const deleteRoute = "DELETE /api/servers/servers/550e8400-e29b-41d4-a716-446655440000/"
	tests := []struct {
		name        string
		servers     string
		wantCode    int
		wantFailure string
		wantDeletes int
	}{
		{name: "deletes the named server", servers: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-server"}]}`, wantDeletes: 1},
		{name: "unknown server", servers: `{"count": 0, "results": []}`, wantCode: utils.ExitCodeGeneralError, wantFailure: "Failed to delete the server"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := cmdtest.Server(t, map[string]cmdtest.Reply{
				"GET /api/servers/servers/": {Status: http.StatusOK, Body: tt.servers},
				deleteRoute:                 {Status: http.StatusNoContent},
			})

			failure, code := cmdtest.Run(newServerDeleteCmd(ts.Factory(), utils.Stdio), "my-server", "-y")

			assert.Equal(t, tt.wantCode, code, failure)
			assert.Contains(t, failure, tt.wantFailure)
			assert.Equal(t, tt.wantDeletes, ts.Hits(deleteRoute))
		})
	}
}
