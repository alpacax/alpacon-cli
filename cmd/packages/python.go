package packages

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var pythonCmd = &cobra.Command{
	Use:   "python",
	Short: "Python packages",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	pythonCmd.AddCommand(pythonPackageListCmd)
	pythonCmd.AddCommand(pythonPackageUploadCmd)
	pythonCmd.AddCommand(pythonPackageDownloadCmd)
}
