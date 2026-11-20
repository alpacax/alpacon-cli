package packages

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newPythonCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "python",
		Short: "Manage Python packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newPythonPackageListCmd(newClient, ios),
		newPythonPackageUploadCmd(newClient, ios),
		newPythonPackageDownloadCmd(newClient, ios),
	)
	return cmd
}
