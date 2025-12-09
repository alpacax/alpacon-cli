package exec

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var ExecCmd = &cobra.Command{
	Use:   "exec [USER@]SERVER COMMAND...",
	Short: "Execute a command on a remote server",
	Long: `Execute a command on a remote server.
	
	This command executes a specified command on a remote server and returns the output.
	It supports SSH-like syntax for specifying the user and server.
	
	Examples:
	  alpacon exec prod-docker docker ps
	  alpacon exec root@prod-docker docker ps
	  alpacon exec admin@web-server ls -la /var/log
	  alpacon exec -u root prod-docker systemctl status nginx
	  alpacon exec -g docker user@server docker images
	`,
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		username, _ := cmd.Flags().GetString("username")
		groupname, _ := cmd.Flags().GetString("groupname")

		if len(args) < 2 {
			utils.CliErrorWithExit("You must specify at least a server name and a command.")
			return
		}

		serverName := args[0]
		commandArgs := args[1:]

		// Parse SSH-like syntax for user@host
		if strings.Contains(serverName, "@") && !strings.Contains(serverName, ":") {
			sshTarget := utils.ParseSSHTarget(serverName)
			if username == "" && sshTarget.User != "" {
				username = sshTarget.User
			}
			serverName = sshTarget.Host
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		command := strings.Join(commandArgs, " ")
		env := make(map[string]string) // Empty env map for now, could be extended with --env flags

		result, err := event.RunCommand(alpaconClient, serverName, command, username, groupname, env)
		if err != nil {
			err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
				OnMFARequired: func(srv string) error {
					return mfa.HandleMFAError(alpaconClient, srv)
				},
				OnUsernameRequired: func() error {
					_, err := iam.HandleUsernameRequired()
					return err
				},
				RetryOperation: func() error {
					result, err = event.RunCommand(alpaconClient, serverName, command, username, groupname, env)
					return err
				},
			})

			if err != nil {
				utils.CliErrorWithExit("Failed to execute command on '%s' server: %s.", serverName, err)
				return
			}
		}
		fmt.Println(result)
	},
}

func init() {
	ExecCmd.Flags().StringP("username", "u", "", "Specify username for command execution")
	ExecCmd.Flags().StringP("groupname", "g", "", "Specify groupname for command execution")
}
