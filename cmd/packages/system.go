package packages

import (
	"errors"
	"github.com/spf13/cobra"
)

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "System packages",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon package system list', 'alpacon package system upload', or 'alpacon package system download'. Run 'alpacon package system --help' for more information")
	},
}

func init() {
	systemCmd.AddCommand(systemPackageListCmd)
	systemCmd.AddCommand(systemPackageUploadCmd)
	systemCmd.AddCommand(systemPackageDownloadCmd)
}
