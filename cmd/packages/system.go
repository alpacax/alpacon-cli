package packages

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "System packages",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	systemCmd.AddCommand(systemPackageListCmd)
	systemCmd.AddCommand(systemPackageUploadCmd)
	systemCmd.AddCommand(systemPackageDownloadCmd)
}
