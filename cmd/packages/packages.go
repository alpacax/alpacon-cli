package packages

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var PackagesCmd = &cobra.Command{
	Use:     "package",
	Aliases: []string{"packages"},
	Short:   "Manage system and Python packages",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	PackagesCmd.AddCommand(systemCmd)
	PackagesCmd.AddCommand(pythonCmd)
}
