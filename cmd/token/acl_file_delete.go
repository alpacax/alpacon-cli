package token

import (
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclFileDeleteCmd = &cobra.Command{
	Use:     "delete ACL_ID",
	Aliases: []string{"rm"},
	Short:   "Delete a file ACL rule by ID",
	Example: `  alpacon token acl file delete 550e8400-e29b-41d4-a716-446655440000
  alpacon token acl file rm 550e8400-e29b-41d4-a716-446655440000`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(runFileAclDelete),
}

func init() {
	aclFileDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

func runFileAclDelete(cmd *cobra.Command, args []string) error {
	aclID := args[0]
	yes, _ := cmd.Flags().GetBool("yes")

	if !yes {
		if proceed, err := utils.Confirm("Delete file ACL '%s'?", aclID); err != nil || !proceed {
			return err
		}
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %v. Consider re-logging.", err)
	}

	if err = security.DeleteFileAcl(alpaconClient, aclID); err != nil {
		return utils.NewExitError("Failed to delete file ACL: %v.", err)
	}

	utils.CliSuccess("File ACL deleted: %s", aclID)
	return nil
}
