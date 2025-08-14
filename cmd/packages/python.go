package packages

import (
	"errors"
	"github.com/spf13/cobra"
)

var pythonCmd = &cobra.Command{
	Use:   "python",
	Short: "Python packages",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon package python list', 'alpacon package python upload', or 'alpacon package python download'. Run 'alpacon package python --help' for more information")
	},
}

func init() {
	pythonCmd.AddCommand(pythonPackageListCmd)
	pythonCmd.AddCommand(pythonPackageUploadCmd)
	pythonCmd.AddCommand(pythonPackageDownloadCmd)
}
