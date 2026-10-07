package authority

import (
	"errors"

	"github.com/spf13/cobra"
)

var AuthorityCmd = &cobra.Command{
	Use:   "authority",
	Short: "Manage private certificate authorities",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Run 'alpacon authority --help' for more information")
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
