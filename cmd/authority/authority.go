package authority

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var AuthorityCmd = &cobra.Command{
	Use:   "authority",
	Short: "Manage private certificate authorities",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	AuthorityCmd.AddCommand(authorityCreateCmd)
	AuthorityCmd.AddCommand(authorityListCmd)
	AuthorityCmd.AddCommand(authorityDetailCmd)
	AuthorityCmd.AddCommand(authorityDownloadCmd)
	AuthorityCmd.AddCommand(authorityDeleteCmd)
	AuthorityCmd.AddCommand(authorityUpdateCmd)
	AuthorityCmd.AddCommand(authorityDownloadCrlCmd)
}
