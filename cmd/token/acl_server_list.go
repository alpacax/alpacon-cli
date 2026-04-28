package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclServerListCmd = &cobra.Command{
	Use:     "ls TOKEN",
	Aliases: []string{"list"},
	Short:   "List server ACL rules for a token",
	Example: `  alpacon token acl server ls my-api-token
  alpacon token acl server list my-api-token`,
	Args: cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		tokenID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if !utils.IsUUID(tokenID) {
			tokenID, err = auth.GetAPITokenIDByName(alpaconClient, tokenID)
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve server ACLs: %s.", err)
			}
		}

		acls, err := security.GetServerAclList(alpaconClient, tokenID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve server ACLs: %s.", err)
		}

		utils.PrintTable(acls)
	},
}
