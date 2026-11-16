package edit

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

const (
	downloadID = "550e8400-e29b-41d4-a716-446655440000"
	commandID  = "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47"
)

// editServer serves the download of /etc/app.conf and counts upload creates; createStatus
// is what the download create answers.
func editServer(t *testing.T, createStatus int, uploads *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/servers/servers/":
			_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47", "name": "my-server"}]}`))
		case "POST /api/webftp/downloads/":
			w.WriteHeader(createStatus)
			_, _ = w.Write([]byte(`{"id": "` + downloadID + `", "command": "` + commandID + `", "download_url": "http://` + r.Host + `/files/app.conf", "code": "permission_denied", "detail": "denied"}`))
		case "GET /api/events/commands/" + commandID + "/":
			_, _ = w.Write([]byte(`{"id": "` + commandID + `", "status": "success", "success": true}`))
		case "GET /files/app.conf":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("listen 80\n"))
		case "GET /api/webftp/downloads/" + downloadID + "/status/":
			_, _ = w.Write([]byte(`{"success": true, "message": "downloaded"}`))
		case "POST /api/webftp/uploads/":
			uploads.Add(1)
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// Serial: it sets HOME, which the auth method and WorkSession lookups read.
func TestEditLeavesAnUnchangedFileAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads it on Windows
	t.Setenv("ALPACON_WORK_SESSION", "")
	var uploads atomic.Int32
	ts := editServer(t, http.StatusCreated, &uploads)

	_, stderr, code := cmdtest.RunCaptured(t, newEditCmd(cmdtest.ClientFactory(ts)), "my-server:/etc/app.conf", "--editor", "true")

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "No changes")
	assert.Zero(t, uploads.Load())
}

// Serial: it sets HOME, which the auth method and WorkSession lookups read.
func TestEditFails(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		noLogin  bool
		wantCode int
		want     string
	}{
		{name: "target without a path", target: "my-server", noLogin: true, wantCode: utils.ExitCodeUsageError, want: "SERVER:PATH"},
		{name: "no login", target: "my-server:/etc/app.conf", noLogin: true, wantCode: utils.ExitCodeGeneralError, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server refuses the download", target: "my-server:/etc/app.conf", wantCode: utils.ExitCodeGeneralError, want: "Failed to edit 'my-server:/etc/app.conf'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("ALPACON_WORK_SESSION", "")
			var uploads atomic.Int32
			newClient := cmdtest.ClientFactory(editServer(t, http.StatusForbidden, &uploads))
			if tt.noLogin {
				newClient = cmdtest.NoClient
			}

			_, stderr, code := cmdtest.RunCaptured(t, newEditCmd(newClient), tt.target, "--editor", "true")

			assert.Equal(t, tt.wantCode, code, stderr)
			assert.Contains(t, stderr, tt.want)
			assert.Zero(t, uploads.Load())
		})
	}
}
