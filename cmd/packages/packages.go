package packages

import (
	"errors"
	"github.com/spf13/cobra"
)

var PackagesCmd = &cobra.Command{
	Use:     "package",
	Aliases: []string{"packages"},
	Short:   "Commands to manage and interact with packages",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon package system' or 'alpacon package python' to manage packages. Run 'alpacon package --help' for more information")
	},
}

func init() {
	PackagesCmd.AddCommand(systemCmd)
	PackagesCmd.AddCommand(pythonCmd)
}
