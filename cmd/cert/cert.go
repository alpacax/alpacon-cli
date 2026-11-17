package cert

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// clientFactory builds the API client a command talks to.
type clientFactory = func() (*client.AlpaconClient, error)

var CertCmd = &cobra.Command{
	Use:     "cert",
	Aliases: []string{"certificate"},
	Short:   "List, describe, and download certificates",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	CertCmd.AddCommand(certListCmd)
	CertCmd.AddCommand(certDetailCmd)
	CertCmd.AddCommand(certDownloadCmd)
}
