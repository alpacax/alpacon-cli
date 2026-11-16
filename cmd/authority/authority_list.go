package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List certificate authorities",
	Long: `
 	Displays a comprehensive list of all certificate authorities that have been initialized within the system, 
	including their status and configuration details
	`,
	Example: `
	alpacon authority ls
	alpacon authority list
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityList, err := cert.GetAuthorityList(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the authority list: %s.", err)
		}

		return utils.PrintTable(authorityList)
	}),
}
