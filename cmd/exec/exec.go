package exec

import (
	"fmt"
	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"strings"
	"time"
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
			utils.CliError("You must specify at least a server name and a command.")
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
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		command := strings.Join(commandArgs, " ")
		env := make(map[string]string) // Empty env map for now, could be extended with --env flags

		result, err := event.RunCommand(alpaconClient, serverName, command, username, groupname, env)
		if err != nil {
			code, _ := utils.ParseErrorResponse(err)
			if code == utils.CodeAuthMFARequired {
				err := mfa.HandleMFAError(alpaconClient, serverName)
				if err != nil {
					utils.CliError("MFA authentication failed: %s", err)
				}

				for {
					fmt.Println("Waiting for MFA authentication...")
					time.Sleep(5 * time.Second)

					result, err = event.RunCommand(alpaconClient, serverName, command, username, groupname, env)
					if err == nil {
						fmt.Println("MFA authentication has been completed!")
						break
					}
				}
			} else {
				utils.CliError("Failed to execute command on '%s' server: %s.", serverName, err)
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
