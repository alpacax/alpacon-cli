package token

import (
	"github.com/alpacax/alpacon-cli/api/security"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var aclDeleteCmd = &cobra.Command{
	Use:     "delete",
	Aliases: []string{"rm"},
	Short:   "Delete the specified command ACL from an API token.",
	Long: `
	Removes an existing command acl from the API token
	This command requires the command acl id to identify the command acl to be deleted.
	`,
	Example: `
	alpacon token acl delete 550e8400-e29b-41d4-a716-446655440000
	alpacon token acl rm 550e8400-e29b-41d4-a716-446655440000
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		commandAclId := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete command ACL '%s'?", commandAclId)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = security.DeleteCommandAcl(alpaconClient, commandAclId)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the command acl: %s.", err)
		}

		utils.CliSuccess("Command ACL deleted: %s", commandAclId)
	},
}

func init() {
	aclDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
