package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenDuplicateCmd = &cobra.Command{
	Use:   "duplicate TOKEN",
	Short: "Duplicate an api token and its rules",
	Long: `
	Creates a copy of an existing API token, including all scopes and ACL rules
	(Command, Server, File). The source token is identified by name or UUID.
	`,
	Example: `
	alpacon token duplicate my-api-token
	alpacon token duplicate my-api-token --name "my-api-token-copy"
	alpacon token duplicate 550e8400-e29b-41d4-a716-446655440000
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tokenID := args[0]
		name, _ := cmd.Flags().GetString("name")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if !utils.IsUUID(tokenID) {
			tokenID, err = auth.GetAPITokenIDByName(alpaconClient, tokenID)
			if err != nil {
				utils.CliErrorWithExit("Failed to duplicate the API token: %s.", err)
			}
		}

		key, err := auth.DuplicateAPIToken(alpaconClient, tokenID, name)
		if err != nil {
			utils.CliErrorWithExit("Failed to duplicate the API token: %s.", err)
		}

		utils.CliSuccess("API token duplicated: %s", key)
		utils.CliWarning("This token cannot be retrieved again after you exit.")
	},
}

func init() {
	tokenDuplicateCmd.Flags().StringP("name", "n", "", "Name for the new token (optional)")
}
