package ftp

import (
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const commandPollPath = "/api/events/commands/cmd-1/"

// A download's command poll refused for MFA reaches the transfer's MFA wait
// instead of backing off silently until the poll's own timeout: one link, the
// poll runs again after the wait, and the download is never created twice.
func TestDownload_CommandPollRefusedForMFAWaitsAndResumes(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name      string
		script    []int
		wantLinks int32
		wantHits  int32
	}{
		{name: "refused once", script: []int{stMFA, stOK}, wantLinks: 1, wantHits: 2},
		// Still refused while the wait retries the poll: the same wait goes on.
		{name: "refused again during the wait", script: []int{stMFA, stMFA, stOK}, wantLinks: 1, wantHits: 3},
		// Not MFA: the poll backs off and goes on, with no link.
		{name: "other error backs off", script: []int{http.StatusInternalServerError, stOK}, wantLinks: 0, wantHits: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &lapseStub{
				method: http.MethodGet, path: commandPollPath, script: tc.script,
				okBody: `{"id": "cmd-1", "status": "completed", "success": true}`, blob: "content",
			}
			ac := stub.client()

			var err error
			synctest.Test(t, func(t *testing.T) {
				_, err = DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
			})

			require.NoError(t, err)
			assert.Equal(t, int32(1), stub.creates.Load(), "the download must be created once")
			assert.Equal(t, tc.wantLinks, stub.links.Load())
			assert.Equal(t, tc.wantHits, stub.hits.Load())
		})
	}
}

// A poll that MFA never clears ends with the refusal after the wait gives up,
// not after the poll's own 30-minute timeout, and issues one link.
func TestDownload_CommandPollRefusedAfterEveryWaitEnds(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	stub := &lapseStub{method: http.MethodGet, path: commandPollPath, script: []int{stMFA}}
	ac := stub.client()

	var err error
	synctest.Test(t, func(t *testing.T) {
		_, err = DownloadFileToPath(ac, "my-server", "/home/alice/a.txt", filepath.Join(t.TempDir(), "a.txt"), "", "", "")
	})

	require.Error(t, err)
	assert.Equal(t, int32(1), stub.creates.Load())
	assert.Equal(t, int32(1), stub.links.Load(), "one link for the one wait")
}
