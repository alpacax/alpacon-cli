package packages

import (
	"github.com/alpacax/alpacon-cli/api/packages"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var systemPackageListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List system packages",
	Long: `
	Display a detailed list of all system packages registered in Alpacon.
	This command provides information such as name, version, platform and other relevant details.
	`,
	Example: `
	alpacon package system ls
	alpacon package system list
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		packageList, err := packages.GetSystemPackageEntry(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the system packages: %s.", err)
		}

		return utils.PrintTable(packageList)
	}),
}
