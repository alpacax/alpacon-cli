package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "all"},
	Short:   "Display a list of all api tokens",
	Long: `
	Displays a list of all API tokens issued. 
	This command provides an overview of token names, creation dates, and expiration dates, helping you manage access effectively.
	`,
	Example: `
	alpacon token ls
	alpacon token list
	alpacon token all
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		tokenList, err := auth.GetAPITokenList(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the api token list: %s.", err)
		}

		utils.PrintTable(tokenList)
	},
}
