package cert

import (
	"errors"
	"github.com/spf13/cobra"
)

var CertCmd = &cobra.Command{
	Use:     "cert",
	Aliases: []string{"certificate"},
	Short:   "Manage and interact with SSL/TLS certificates",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon cert ls', 'alpacon cert describe', or 'alpacon cert download' to manage SSL/TLS certificates. Run 'alpacon cert --help' for more information")
	},
}

func init() {
	CertCmd.AddCommand(certListCmd)
	CertCmd.AddCommand(certDetailCmd)
	CertCmd.AddCommand(certDownloadCmd)
}
