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
	Short:   "Show details of a CA",
	Long: `
	The describe command fetches and displays detailed information about a specific certificate authority,
	including its crt text, organization and other relevant attributes.
	`,
	Example: `
	alpacon authority describe "Root CA"
	alpacon authority desc my-authority
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		authorityName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityID, err := cert.GetAuthorityIDByName(alpaconClient, authorityName)
		if err != nil {
			return utils.NewExitError("Failed to find authority: %s.", err)
		}

		authorityDetail, err := cert.GetAuthorityDetail(alpaconClient, authorityID)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the authority details: %s.", err)
		}

		return utils.PrintJson(authorityDetail)
	}),
}
