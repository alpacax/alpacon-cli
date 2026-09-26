// Package removed registers hidden stubs for commands that no longer exist, so
// an old script or muscle memory gets guidance instead of "unknown command".
//
// TODO(remove): drop these stubs in the release after v1.14.0 (#477).
package removed

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// serverPlaceholder stands in for the server name when the old invocation did
// not carry one.
const serverPlaceholder = "<server>"

// Command returns a hidden command at use that prints guidance and exits with
// utils.ExitCodeCommandRemoved. guidance may contain serverPlaceholder, which
// is replaced by the first positional argument of the old invocation.
//
// Flag parsing is disabled so any old argument or flag (-y, --force) still
// reaches Run; that also routes --help to the same guidance. Run never builds
// an API client.
func Command(name, path, guidance string) *cobra.Command {
	return &cobra.Command{
		Use:                name + " [SERVER] [flags]",
		Short:              "Explain the replacement command",
		Long:               Message(path, guidance, nil),
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		Run: func(cmd *cobra.Command, args []string) {
			utils.CliErrorWithExitCode(utils.ExitCodeCommandRemoved, "%s", Message(path, guidance, args))
		},
	}
}

// Message renders the guidance for a removed command path, naming the server
// from args when the old invocation gave one.
func Message(path, guidance string, args []string) string {
	server := serverPlaceholder
	for _, arg := range args {
		if arg != "" && !strings.HasPrefix(arg, "-") {
			server = arg
			break
		}
	}
	return fmt.Sprintf("%q was removed. %s", path, strings.ReplaceAll(guidance, serverPlaceholder, server))
}
