package exec

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
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

Shell metacharacters (;, |, &, $) pass through unquoted to the remote shell.
To send a literal metacharacter, wrap the argument in quotes:
  alpacon exec server 'echo hello;world'

Commands larger than 2KB are sent with an oversized flag; the server stages
them as a temporary script and runs them, judging the original command content.

Flags:
  -u, --username [USER_NAME]    Specify the username for command execution.
  -g, --groupname [GROUP_NAME]  Specify the group name for command execution.
  --work-session [UUID]         Attach this command to a work-session.
                                Overrides the workspace's active session set via
                                'alpacon work-session use'.
  --detach                      Submit the command and return immediately without
                                waiting for completion. Prints the job ID to stdout.
                                Use 'alpacon exec logs JOB_ID' to retrieve the result.

Exit code 3 indicates a WorkSession gate denial; run with --output json to
parse a machine-readable diagnostic on stderr.
Requires an active WorkSession when using Browser login (Auth0); Token auth (API token or Service token) bypasses this requirement.`,
	Example: `  # Simple command execution
  alpacon exec prod-docker docker ps
  alpacon exec root@prod-docker docker ps

  # Use -- to pass flags to the remote command
  alpacon exec root@db-server -- docker exec postgres psql -U myproject -d myproject
  alpacon exec my-server -- grep -r "pattern" /var/log

  # Specify user and group with flags
  alpacon exec -u root prod-docker systemctl status nginx
  alpacon exec -g docker user@server docker images

  # Submit a command asynchronously and retrieve the result later
  alpacon exec --detach web-server -- apt-get update
  alpacon exec logs <JOB_ID>`,
	// DisableFlagParsing is required so remote command args (e.g. -U, -d) aren't consumed by Cobra; all flags are parsed manually in Run.
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

		if parsed.OutputFormat != "" {
			if parsed.OutputFormat != utils.OutputFormatTable && parsed.OutputFormat != utils.OutputFormatJSON {
				utils.CliErrorWithExit("invalid --output value %q: must be 'table' or 'json'", parsed.OutputFormat)
			}
			utils.OutputFormat = parsed.OutputFormat
		}

		workSessionID := worksession.ResolveOrExit(parsed.WorkSessionID)

		authMethod := config.ResolveAuthMethod()

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		env := make(map[string]string)

		// Oversized commands are sent with the same command-create call plus the
		// oversized flag; the server stages them as a temp script and owns the
		// path/wrapper/cleanup, auth, and platform gates.
		oversized := ResolveOversized(alpaconClient, parsed.Server, parsed.Command)

		if parsed.Detach {
			runDetached(alpaconClient, parsed, parsed.Command, env, workSessionID, authMethod, oversized)
			return
		}

		result, err := RunCommandWithRetry(alpaconClient, parsed.Server, parsed.Command, parsed.Username, parsed.Groupname, env, workSessionID, oversized)
		utils.HandleWorkSessionError(err, "command", parsed.Server, authMethod, workSessionID)
		HandleCommandResult(result, err)
	},
}
