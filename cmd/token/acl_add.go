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
	Short: "Add a new command ACL with specific token and command.",
	Long: `
	The add command allows you to define access to specific commands for API tokens.
	`,
	Example: `
	alpacon token acl add
	alpacon token acl add --token=my-api-token --command="server ls"
	`,
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
	aclAddCmd.Flags().StringVarP(&command, "command", "c", "", "Command")
}

func promptForAcl() security.CommandAclRequest {
	var commandAclRequest security.CommandAclRequest

	commandAclRequest.Token = utils.PromptForRequiredInput("Token ID or name: ")
	commandAclRequest.Command = utils.PromptForRequiredInput("Command: ")

	return commandAclRequest
}
