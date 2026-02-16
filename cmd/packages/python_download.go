package packages

import (
	"github.com/alpacax/alpacon-cli/api/packages"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var pythonPackageDownloadCmd = &cobra.Command{
	Use:   "download [PACKAGE NAME] [FILE PATH]",
	Short: "Download a python package from alpacon",
	Long: `
	The 'download' command allows users to download a Python package from the alpacon.
	This command is designed to facilitate the transfer of your locally developed Python packages to a remote server environment for further usage or distribution.
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
			utils.CliErrorWithExit("Failed to download the python packages from alpacon: %s.", err)
		}

		utils.CliSuccess("Package downloaded: %s", file)
	},
}
