package token

import (
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclCommandDeleteCmd = &cobra.Command{
	Use:     "delete ACL_ID",
	Aliases: []string{"rm"},
	Short:   "Delete a command ACL rule by ID",
	Long: `Delete a command ACL rule by ID.

On servers that manage command ACLs on the command pre-approval ledger, this
write is refused with command_acl_moved; a Command ACL admin revokes the entry
under Pre-approved commands on the service token's page in the web console, and a
personal API token has no command list on the ledger.`,
	Example: `  alpacon token acl command delete 550e8400-e29b-41d4-a716-446655440000
  alpacon token acl command rm 550e8400-e29b-41d4-a716-446655440000`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(runCommandAclDelete),
}

func init() {
	aclCommandDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

func runCommandAclDelete(cmd *cobra.Command, args []string) error {
	aclID := args[0]
	yes, _ := cmd.Flags().GetBool("yes")

	if !yes {
		if proceed, err := utils.Confirm("Delete command ACL '%s'?", aclID); err != nil || !proceed {
			return err
		}
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	if err = security.DeleteCommandAcl(alpaconClient, aclID); err != nil {
		return utils.NewExitError("Failed to delete command ACL: %v.", err)
	}

	utils.CliSuccess("Command ACL deleted: %s", aclID)
	return nil
}
