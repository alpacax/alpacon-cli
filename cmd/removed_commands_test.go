package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

// The four paths below were removed from the CLI. They have no command of
// their own, so the parent group takes the arguments, prints its help and fails
// with the general error code; an old flag such as -y fails as an unknown flag.
func TestRemovedCommandPathsAreNotCommands(t *testing.T) {
	t.Parallel()
	paths := [][]string{
		{"server", "reboot"},
		{"server", "shutdown"},
		{"server", "upgrade"},
		{"agent", "shutdown"},
	}
	for _, path := range paths {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			t.Parallel()
			for _, extra := range [][]string{{"my-server"}, {"my-server", "-y"}} {
				args := append(append([]string{}, path...), extra...)
				stdout, stderr, exitCode := runHelperProcess(t, "TestRemovedCommandsHelperProcess", "removed-helper", args,
					"GO_WANT_REMOVED_HELPER=1", homeEnvVar()+"="+t.TempDir())

				assert.Equal(t, utils.ExitCodeGeneralError, exitCode, "%v", args)
				assert.NotContains(t, stdout+stderr, "was removed", "%v", args)
				if len(extra) == 1 {
					assert.Contains(t, stdout, "Available Commands:", "%v", args)
					assert.Contains(t, stderr, "a subcommand is required", "%v", args)
				} else {
					assert.Contains(t, stderr, "unknown shorthand flag: 'y'", "%v", args)
				}
			}
		})
	}
}

func TestRemovedCommandsHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_REMOVED_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "removed-helper")
	if !ok {
		os.Exit(2)
	}
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}
