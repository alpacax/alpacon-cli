package token

import (
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclCommandDeleteCmd = &cobra.Command{
	Use:     "delete ACL-ID",
	Aliases: []string{"rm"},
	Short:   "Delete a command ACL rule by ID",
	Example: `  alpacon token acl command delete 550e8400-e29b-41d4-a716-446655440000
  alpacon token acl command rm 550e8400-e29b-41d4-a716-446655440000`,
	Args: cobra.ExactArgs(1),
	Run:  runCommandAclDelete,
}

func init() {
	aclCommandDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

func runCommandAclDelete(cmd *cobra.Command, args []string) {
	aclID := args[0]
	yes, _ := cmd.Flags().GetBool("yes")

	if !yes {
		utils.ConfirmAction("Delete command ACL '%s'?", aclID)
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	if err = security.DeleteCommandAcl(alpaconClient, aclID); err != nil {
		utils.CliErrorWithExit("Failed to delete command ACL: %v.", err)
	}

	utils.CliSuccess("Command ACL deleted: %s", aclID)
}
