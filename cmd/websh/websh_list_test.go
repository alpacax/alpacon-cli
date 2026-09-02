package websh

import (
	"os"
	osexec "os/exec"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DisableFlagParsing only covers the parent; this pins that a child still routes and parses flags normally.
func TestWebshLsRoutesAndParsesFlags(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectedTail int
	}{
		{
			name:         "defaults",
			args:         []string{"ls"},
			expectedTail: 25,
		},
		{
			name:         "long flag",
			args:         []string{"ls", "--tail", "50"},
			expectedTail: 50,
		},
		{
			// The parent's hand-rolled -s and -u never reach here, since routing to the subcommand happens first.
			name:         "short flag",
			args:         []string{"ls", "-t", "5"},
			expectedTail: 5,
		},
		{
			name:         "list alias",
			args:         []string{"list", "--tail", "7"},
			expectedTail: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, flags, err := WebshCmd.Find(tt.args)
			require.NoError(t, err)
			require.Equal(t, "ls", cmd.Name())

			// Reset from the flag's own default so a prior case cannot leak in and the defaults case still checks what is registered.
			require.NoError(t, cmd.Flags().Set("tail", cmd.Flags().Lookup("tail").DefValue))

			require.NoError(t, cmd.ParseFlags(flags))

			tail, err := cmd.Flags().GetInt("tail")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedTail, tail)
		})
	}
}

func TestWebshLsTailZeroExitsWithUsageErrorCode(t *testing.T) {
	t.Parallel()
	helper := osexec.Command(os.Args[0], "-test.run=^TestWebshLsTailZeroHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_WEBSH_LS_TAIL_HELPER=1")

	err := helper.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodeUsageError, exitErr.ExitCode())
}

// Serial: the child process reaches an os.Exit, and the parent's own run of this
// function is a two-line guard that parallelism buys nothing for.
func TestWebshLsTailZeroHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WEBSH_LS_TAIL_HELPER") != "1" {
		return
	}
	runWebshList(0)
}
