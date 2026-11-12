package server

import (
	serverAPI "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List server registration tokens",
	Long: `Display all server registration tokens.
Tokens are used by Alpamon to self-register servers without manual intervention.`,
	Example: `
    alpacon server token ls
    alpacon server token list
    alpacon server token ls --output json`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		tokens, err := serverAPI.GetRegistrationTokenAttributes(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the registration tokens: %s.", err)
		}

		utils.PrintTable(tokens)
		return nil
	}),
}
