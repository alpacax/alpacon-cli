package note

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoteLsParsesFlags(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		expectedTail   int
		expectedServer string
		expectedPinned bool
	}{
		{
			name:         "defaults",
			args:         []string{"ls"},
			expectedTail: 25,
		},
		{
			name:           "long flags",
			args:           []string{"ls", "--tail", "100", "--server", "my-server", "--pinned"},
			expectedTail:   100,
			expectedServer: "my-server",
			expectedPinned: true,
		},
		{
			name:           "short flags",
			args:           []string{"ls", "-t", "5", "-s", "other-server"},
			expectedTail:   5,
			expectedServer: "other-server",
		},
		{
			name:         "list alias",
			args:         []string{"list", "--tail", "7"},
			expectedTail: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, flags, err := NoteCmd.Find(tt.args)
			require.NoError(t, err)
			require.Equal(t, "ls", cmd.Name())

			// Reset from each flag's own default so a prior case cannot leak in and the defaults case still checks what is registered.
			for _, name := range []string{"tail", "server", "pinned"} {
				require.NoError(t, cmd.Flags().Set(name, cmd.Flags().Lookup(name).DefValue))
			}

			require.NoError(t, cmd.ParseFlags(flags))

			parsedTail, err := cmd.Flags().GetInt("tail")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedTail, parsedTail)

			parsedServer, err := cmd.Flags().GetString("server")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedServer, parsedServer)

			parsedPinned, err := cmd.Flags().GetBool("pinned")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedPinned, parsedPinned)
		})
	}
}

func TestNoteLsTailZeroExitsWithUsageErrorCode(t *testing.T) {
	t.Parallel()

	err := runNoteList(cmdtest.NoClient, 0, "", false)

	var exitErr *utils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodeUsageError, exitErr.Code)
	var stderr bytes.Buffer
	utils.FprintExitError(&stderr, exitErr)
	assert.Contains(t, stderr.String(), "--tail must be a positive integer.")
}

// Serial: it captures stdout and sets utils.OutputFormat.
func TestNoteLsPrintsTheNotes(t *testing.T) {
	ts := cmdtest.Server(t, map[string]cmdtest.Reply{
		"GET /api/servers/notes/": {Status: http.StatusOK, Body: `{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "server": {"name": "my-server"}, "author": {"name": "alice"}, "content": "rotate the disk", "pinned": true, "added_at": "2026-11-01T09:00:00Z"}]}`},
	})

	stdout, stderr, code := cmdtest.RunJSON(t, newNoteListCmd(ts.Factory()))

	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"rotate the disk"`)
	assert.Contains(t, stdout, `"my-server"`)
}

func TestNoteLsFailsWithExit1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		noLogin bool
		want    string
	}{
		{name: "no login", noLogin: true, want: "Connection to Alpacon API failed: not logged in"},
		{name: "server error", want: "Failed to retrieve the notes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newClient := cmdtest.NoClient
			if !tt.noLogin {
				newClient = cmdtest.Server(t, map[string]cmdtest.Reply{
					"GET /api/servers/notes/": {Status: http.StatusInternalServerError, Body: `{"detail": "unavailable"}`},
				}).Factory()
			}

			failure, code := cmdtest.Run(newNoteListCmd(newClient))

			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, failure, tt.want)
		})
	}
}
