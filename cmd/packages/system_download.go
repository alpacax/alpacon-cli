package packages

import (
	"github.com/alpacax/alpacon-cli/api/packages"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var systemPackageDownloadCmd = &cobra.Command{
	Use:   "download PACKAGE DIRECTORY",
	Short: "Download a system package from Alpacon",
	Long: `
	The 'download' command allows users to download a system package from Alpacon.
	The package is saved into DIRECTORY on this machine.
	`,
	Example: `
	alpacon package system download osquery_5.8.2-1.linux_amd64.deb .
	`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		file := args[0]
		dest := args[1]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = packages.DownloadPackage(alpaconClient, file, dest, "system")
		if err != nil {
			utils.CliErrorWithExit("Failed to download the system package from Alpacon: %s.", err)
		}

		utils.CliSuccess("Package downloaded: %s", file)
	},
}
