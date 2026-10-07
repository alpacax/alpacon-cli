package csr

import (
	"errors"

	"github.com/spf13/cobra"
)

var CsrCmd = &cobra.Command{
	Use:   "csr",
	Short: "Manage certificate signing requests (CSRs)",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Run 'alpacon csr --help' for more information")
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
