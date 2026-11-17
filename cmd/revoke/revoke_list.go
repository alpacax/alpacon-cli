package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeListCmd = newRevokeListCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newRevokeListCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List certificate revoke requests",
		Long: `
	List all certificate revoke requests with optional filtering by status or certificate.
	`,
		Example: `
	alpacon revoke list
	alpacon revoke ls --status=requested
	alpacon revoke ls --certificate=550e8400-e29b-41d4-a716-446655440000
	`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			status, _ := cmd.Flags().GetString("status")
			certificate, _ := cmd.Flags().GetString("certificate")

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			requestList, err := cert.GetRevokeRequestList(alpaconClient, status, certificate)
			if err != nil {
				return utils.NewExitError("Failed to get revoke requests: %s.", err)
			}

			return ios.PrintTable(requestList)
		}),
	}
	cmd.Flags().String("status", "", "Filter by status (requested, revoking, revoked, failed, denied, canceled)")
	cmd.Flags().String("certificate", "", "Filter by certificate ID")
	return cmd
}
