package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclFileListCmd = &cobra.Command{
	Use:     "ls TOKEN",
	Aliases: []string{"list"},
	Short:   "List file ACL rules for a token",
	Example: `  alpacon token acl file ls my-api-token
  alpacon token acl file list my-api-token`,
	Args: cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		tokenID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		tokenID, err = auth.ResolveTokenID(alpaconClient, tokenID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve file ACLs: %s.", err)
		}

		acls, err := security.GetFileAclList(alpaconClient, tokenID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve file ACLs: %s.", err)
		}

		utils.PrintTable(acls)
	},
}
