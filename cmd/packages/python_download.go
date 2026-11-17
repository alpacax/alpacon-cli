package packages

import (
	"github.com/alpacax/alpacon-cli/api/packages"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newPythonPackageDownloadCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
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
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			file := args[0]
			dest := args[1]

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			err = packages.DownloadPackage(alpaconClient, file, dest, "python")
			if err != nil {
				return utils.NewExitError("Failed to download the Python package from Alpacon: %s.", err)
			}

			ios.Success("Package downloaded: %s", file)
			return nil
		}),
	}
}
