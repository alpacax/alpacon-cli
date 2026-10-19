package packages

import (
	"github.com/alpacax/alpacon-cli/api/packages"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var pythonPackageDownloadCmd = &cobra.Command{
	Use:   "download PACKAGE DIRECTORY",
	Short: "Download a Python package from Alpacon",
	Long: `
	The 'download' command allows users to download a Python package from Alpacon.
	The package is saved into DIRECTORY on this machine.
	`,
	Example: `
	alpacon package python download alpamon-1.1.0-py3-none-any.whl .
	`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		file := args[0]
		dest := args[1]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = packages.DownloadPackage(alpaconClient, file, dest, "python")
		if err != nil {
			utils.CliErrorWithExit("Failed to download the Python package from Alpacon: %s.", err)
		}

		utils.CliSuccess("Package downloaded: %s", file)
	},
}
