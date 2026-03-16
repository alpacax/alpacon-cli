package exec

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var ExecCmd = &cobra.Command{
	Use:   "exec [flags] [USER@]SERVER [--] COMMAND...",
	Short: "Execute a command on a remote server",
	Long: `Execute a command on a remote server.

This command executes a specified command on a remote server and returns the output.
It supports SSH-like syntax for specifying the user and server.

Use -- to separate alpacon flags from the remote command, ensuring that flags
intended for the remote command (e.g., -U, -d) are not interpreted as alpacon flags.

All flags must be placed before the server name.

Flags:
  -u, --username [USER_NAME]    Specify the username for command execution.
  -g, --groupname [GROUP_NAME]  Specify the group name for command execution.`,
	Example: `  # Simple command execution
  alpacon exec prod-docker docker ps
  alpacon exec root@prod-docker docker ps

  # Use -- to pass flags to the remote command
  alpacon exec root@db-server -- docker exec postgres psql -U myproject -d myproject
  alpacon exec my-server -- grep -r "pattern" /var/log

  # Specify user and group with flags
  alpacon exec -u root prod-docker systemctl status nginx
  alpacon exec -g docker user@server docker images`,
	// DisableFlagParsing is required because remote command arguments (e.g., -U, -d)
	// would otherwise be consumed by Cobra's flag parser.
	// All flags are parsed manually in the Run function.
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		parsed := ParseRemoteExecArgs(args)

		if parsed.ShowHelp {
			_ = cmd.Help()
			return
		}

		if parsed.Err != "" {
			utils.CliErrorWithExit("%s", parsed.Err)
			return
		}

		if parsed.Server == "" {
			_ = cmd.Help()
			utils.CliErrorWithExit("server name is required.")
			return
		}

		if parsed.Command == "" {
			utils.CliErrorWithExit("You must specify a command to execute.")
			return
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		env := make(map[string]string)
		result, err := RunCommandWithRetry(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env)
		if err != nil {
			utils.CliErrorWithExit("%s", err)
			return
		}
		fmt.Println(result)
	},
}
