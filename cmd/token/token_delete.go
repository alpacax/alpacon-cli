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
	alpacon token delete [TOKEN_ID_OR_NAME]
	alpacon token rm [TOKEN_ID_OR_NAME]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tokenId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if !utils.IsUUID(tokenId) {
			tokenId, err = auth.GetAPITokenIDByName(alpaconClient, tokenId)
			if err != nil {
				utils.CliError("Failed to delete the api token: %s.", err)
			}
		}

		err = auth.DeleteAPIToken(alpaconClient, tokenId)
		if err != nil {
			utils.CliError("Failed to delete the api token: %s.", err)
		}

		utils.CliInfo("API Token successfully deleted: %s", tokenId)
	},
}
