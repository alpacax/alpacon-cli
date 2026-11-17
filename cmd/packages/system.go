package packages

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newSystemCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system",
		Short: "Manage system packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.RequireSubcommand(cmd)
		},
	}
	cmd.AddCommand(
		newSystemPackageListCmd(newClient, ios),
		newSystemPackageUploadCmd(newClient, ios),
		newSystemPackageDownloadCmd(newClient, ios),
	)
	return cmd
}
