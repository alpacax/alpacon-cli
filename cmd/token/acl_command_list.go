package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclCommandListCmd = &cobra.Command{
	Use:     "ls TOKEN",
	Aliases: []string{"list"},
	Short:   "List command ACL rules for a token",
	Example: `  alpacon token acl command ls my-api-token
  alpacon token acl command list my-api-token`,
	Args: cobra.ExactArgs(1),
	Run:  runCommandAclList,
}

func runCommandAclList(_ *cobra.Command, args []string) {
	tokenID := args[0]

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	tokenID, err = auth.ResolveTokenID(alpaconClient, tokenID)
	if err != nil {
		utils.CliErrorWithExit("Failed to resolve token: %v.", err)
	}

	acls, err := security.GetCommandAclList(alpaconClient, tokenID)
	if err != nil {
		utils.CliErrorWithExit("Failed to retrieve command ACLs: %v.", err)
	}

	utils.PrintTable(acls)
}
