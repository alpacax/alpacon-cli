package ftp

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	uploadID           = "550e8400-e29b-41d4-a716-446655440000"
	uploadCreateRoute  = "POST /api/webftp/uploads/"
	uploadTriggerRoute = "GET /api/webftp/uploads/" + uploadID + "/upload/"
)

// cpScene is a source file and a server that accepts its upload. Serial: it sets HOME,
// which the WorkSession lookup reads.
func cpScene(t *testing.T, createStatus int, createBody string) (*cmdtest.Fake, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads it on Windows
	t.Setenv("ALPACON_WORK_SESSION", "")
	source := filepath.Join(home, "app.conf")
	require.NoError(t, os.WriteFile(source, []byte("listen 80"), 0o600))
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/servers/servers/":                        {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47", "name": "my-server"}]}`},
		uploadCreateRoute:                                  {Status: createStatus, Body: createBody},
		uploadTriggerRoute:                                 {Status: http.StatusOK, Body: `{}`},
		"GET /api/webftp/uploads/" + uploadID + "/status/": {Status: http.StatusOK, Body: `{"success": true, "message": "uploaded"}`},
	})
	return ts, source
}

// Serial: cpScene sets HOME.
func TestCpUploadsTheFile(t *testing.T) {
	ts, source := cpScene(t, http.StatusCreated, `{"id": "`+uploadID+`"}`)

	_, stderr, code := cmdtest.RunCaptured(func(ios *utils.IOStreams) *cobra.Command { return newCpCmd(ts.Factory(), ios) }, source, "my-server:/etc/app/")

	assert.Equal(t, 0, code, stderr)
	assert.Equal(t, 1, ts.Hits(uploadCreateRoute))
	assert.Equal(t, 1, ts.Hits(uploadTriggerRoute))
}

// Serial: cpScene sets HOME.
func TestCpFails(t *testing.T) {
	tests := []struct {
		name      string
		newClient func(ts *cmdtest.Fake) func() (*client.AlpaconClient, error)
		wantCode  int
		want      string
	}{
		{
			name:      "no login",
			newClient: func(*cmdtest.Fake) func() (*client.AlpaconClient, error) { return cmdtest.NoClient },
			wantCode:  utils.ExitCodeGeneralError,
			want:      "Connection to Alpacon API failed: not logged in",
		},
		{
			name:      "server refuses the upload",
			newClient: (*cmdtest.Fake).Factory,
			wantCode:  utils.ExitCodeGeneralError,
			want:      "denied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, source := cpScene(t, http.StatusForbidden, `{"code": "permission_denied", "detail": "denied"}`)

			_, stderr, code := cmdtest.RunCaptured(func(ios *utils.IOStreams) *cobra.Command { return newCpCmd(tt.newClient(ts), ios) }, source, "my-server:/etc/app/")

			assert.Equal(t, tt.wantCode, code, stderr)
			assert.Contains(t, stderr, tt.want)
			assert.Zero(t, ts.Hits(uploadTriggerRoute))
		})
	}
}
