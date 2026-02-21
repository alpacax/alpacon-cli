package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDetailCmd = &cobra.Command{
	Use:     "describe AUTHORITY",
	Aliases: []string{"desc"},
	Short:   "Display detailed information about a specific Certificate Authority",
	Long: `
	The describe command fetches and displays detailed information about a specific certificate authority,
	including its crt text, organization and other relevant attributes.
	`,
	Example: `
	alpacon authority describe "Root CA"
	alpacon authority desc my-authority
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		authorityName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityID, err := cert.GetAuthorityIDByName(alpaconClient, authorityName)
		if err != nil {
			utils.CliErrorWithExit("Failed to find authority: %s.", err)
		}

		authorityDetail, err := cert.GetAuthorityDetail(alpaconClient, authorityID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the authority details: %s.", err)
		}

		utils.PrintJson(authorityDetail)
	},
}
