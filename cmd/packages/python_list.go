package packages

import (
	"github.com/alpacax/alpacon-cli/api/packages"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var pythonPackageListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "all"},
	Short:   "Display a list of all python packages",
	Long: `
	Display a detailed list of all python packages registered in the Alpacon.
	This command provides information such as name, version, platform and other relevant details.
	`,
	Example: `
	alpacon package python ls
	alpacon package python list
	alpacon package python all
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		packageList, err := packages.GetPythonPackageEntry(alpaconClient)
		if err != nil {
			utils.CliError("Failed to retrieve the python package: %s.", err)
		}

		utils.PrintTable(packageList)
	},
}
