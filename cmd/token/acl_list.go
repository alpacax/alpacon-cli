package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "Display all command ACLs for an API token",
	Long: `
	This command displays all command access control lists (ACLs) registered to an API token. 
	It shows details such as the token name and the commands associated with each ACL.
	`,
	Example: `
	alpacon token acl ls [TOKEN_ID_OR_NAME]
	alpacon token acl list [TOKEN_ID_OR_NAME]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tokenId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if !utils.IsUUID(tokenId) {
			tokenId, err = auth.GetAPITokenIDByName(alpaconClient, tokenId)
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve the command acl: %s.", err)
			}
		}

		commandAcl, err := security.GetCommandAclList(alpaconClient, tokenId)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the command acl: %s.", err)
		}

		utils.PrintTable(commandAcl)
	},
}
