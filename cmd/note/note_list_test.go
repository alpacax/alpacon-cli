package note

import (
	"os"
	osexec "os/exec"
	"testing"

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
	helper := osexec.Command(os.Args[0], "-test.run=^TestNoteLsTailZeroHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_NOTE_LS_TAIL_HELPER=1")

	err := helper.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodeUsageError, exitErr.ExitCode())
}

// Serial: the child process reaches an os.Exit, and the parent's own run of this
// function is a two-line guard that parallelism buys nothing for.
func TestNoteLsTailZeroHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_NOTE_LS_TAIL_HELPER") != "1" {
		return
	}
	runNoteList(0, "", false)
}
