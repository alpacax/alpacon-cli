package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenScopesCmd = &cobra.Command{
	Use:   "scopes",
	Short: "List available scopes for API tokens",
	Long: `
	Lists all scope resources and their actions that the current user
	has permission to grant when creating an API token.
	`,
	Example: `alpacon token scopes`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		scopes, err := auth.GetTokenScopes(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve available scopes: %s.", err)
		}

		if utils.OutputFormat != utils.OutputFormatJSON {
			for i := range scopes {
				if scopes[i].Actions == "" {
					scopes[i].Actions = "(matches all scopes)"
				}
			}
		}

		utils.PrintTable(scopes)
	},
}
