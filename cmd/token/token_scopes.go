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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		scopes, err := auth.GetTokenScopes(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve available scopes: %s.", err)
		}

		if utils.OutputFormat != utils.OutputFormatJSON {
			for i := range scopes {
				if scopes[i].Actions == "" {
					scopes[i].Actions = "(matches all scopes)"
				}
			}
		}

		return utils.PrintTable(scopes)
	}),
}
