package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new command ACL with specific token and command",
	Long: `Define which commands an API token is allowed to execute.

ACL rules control two types of commands:
  - CLI commands: Alpacon CLI subcommands like "server ls", "websh", or "cp"
  - Server-side commands: Shell commands executed on remote servers via websh or exec
    (e.g., "whoami", "systemctl status *", "docker compose *")

Use * as a wildcard to match any arguments. Without a wildcard, only the exact
command string is matched.`,
	Example: `  # Server-side command ACL: allow executing "whoami" on remote servers via websh/exec
  alpacon token acl add --token=my-api-token --command="whoami"

  # Wildcard: allow "echo" with any arguments (matches "echo hello", "echo foo bar", etc.)
  alpacon token acl add --token=my-api-token --command="echo *"

  # Wildcard: allow "systemctl status" with any service name
  alpacon token acl add --token=my-api-token --command="systemctl status *"

  # Interactive mode (prompts for token and command)
  alpacon token acl add`,
	Run: func(cmd *cobra.Command, args []string) {
		token, _ := cmd.Flags().GetString("token")
		command, _ := cmd.Flags().GetString("command")

		var commandAclRequest security.CommandAclRequest
		if token == "" || command == "" {
			commandAclRequest = promptForAcl()
		} else {
			commandAclRequest = security.CommandAclRequest{
				Token:   token,
				Command: command,
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if !utils.IsUUID(commandAclRequest.Token) {
			commandAclRequest.Token, err = auth.GetAPITokenIDByName(alpaconClient, commandAclRequest.Token)
			if err != nil {
				utils.CliErrorWithExit("Failed to add the command ACL to token: %v.", err)
			}
		}

		err = security.AddCommandAcl(alpaconClient, commandAclRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to add the command ACL to token: %v.", err)
		}

		utils.CliSuccess("Command ACL added to token %s: %s", token, command)
	},
}

func init() {
	var token, command string

	aclAddCmd.Flags().StringVarP(&token, "token", "t", "", "Token ID")
	aclAddCmd.Flags().StringVarP(&command, "command", "c", "", "CLI subcommand or server-side shell command (supports * wildcard)")
}

func promptForAcl() security.CommandAclRequest {
	var commandAclRequest security.CommandAclRequest

	commandAclRequest.Token = utils.PromptForRequiredInput("Token ID or name: ")
	commandAclRequest.Command = utils.PromptForRequiredInput("Command: ")

	return commandAclRequest
}
