package csr

import (
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var CsrCmd = &cobra.Command{
	Use:   "csr",
	Short: "Manage certificate signing requests (CSRs)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	CsrCmd.AddCommand(csrCreateCmd)
	CsrCmd.AddCommand(csrListCmd)
	CsrCmd.AddCommand(csrApproveCmd)
	CsrCmd.AddCommand(csrDenyCmd)
	CsrCmd.AddCommand(csrDeleteCmd)
	CsrCmd.AddCommand(csrDetailCmd)
	CsrCmd.AddCommand(csrDownloadCrtCmd)
	CsrCmd.AddCommand(csrRetryCmd)
}
