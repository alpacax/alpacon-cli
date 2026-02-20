package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenDeleteCmd = &cobra.Command{
	Use:     "delete [TOKEN NAME]",
	Aliases: []string{"rm"},
	Short:   "Delete a specified api token",
	Long: `
	Removes an existing API token from the system. 
	This command requires the token name to identify the token to be deleted.
	`,
	Example: `
	alpacon token delete my-api-token
	alpacon token rm my-api-token
	alpacon token delete my-api-token -y
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tokenId := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete API token '%s'?", tokenId)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if !utils.IsUUID(tokenId) {
			tokenId, err = auth.GetAPITokenIDByName(alpaconClient, tokenId)
			if err != nil {
				utils.CliErrorWithExit("Failed to delete the api token: %s.", err)
			}
		}

		err = auth.DeleteAPIToken(alpaconClient, tokenId)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the api token: %s.", err)
		}

		utils.CliSuccess("API token deleted: %s", tokenId)
	},
}

func init() {
	tokenDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
