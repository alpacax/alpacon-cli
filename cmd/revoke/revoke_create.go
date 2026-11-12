package revoke

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var revokeCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new certificate revoke request",
	Long: `
	Create a new certificate revoke request. You will be prompted for the certificate ID
	and reason if not provided via flags.

	Reason codes:
	  0 = Unspecified           1 = Key Compromise
	  2 = CA Compromise         3 = Affiliation Changed
	  4 = Superseded            5 = Cessation Of Operation
	  6 = Certificate Hold      9 = Privilege Withdrawn
	  10 = AA Compromise
	`,
	Example: `
	alpacon revoke create
	alpacon revoke create --certificate=550e8400-e29b-41d4-a716-446655440000 --reason=1 --requested-reason="Key was compromised"
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		certificate, _ := cmd.Flags().GetString("certificate")
		reason, _ := cmd.Flags().GetInt("reason")
		requestedReason, _ := cmd.Flags().GetString("requested-reason")

		var err error
		if certificate == "" {
			if certificate, err = utils.ReadRequiredInput("Certificate ID: "); err != nil {
				return err
			}
		}
		if !cmd.Flags().Changed("reason") {
			reason = utils.PromptForRequiredIntInput("Reason code (0=Unspecified, 1=Key Compromise, 2=CA Compromise, 3=Affiliation Changed, 4=Superseded, 5=Cessation Of Operation): ")
		}
		if requestedReason == "" {
			if requestedReason, err = utils.ReadInput("Requested reason (description, optional): "); err != nil {
				return err
			}
		}

		request := cert.RevokeRequestCreate{
			Certificate:     certificate,
			Reason:          reason,
			RequestedReason: requestedReason,
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		response, err := cert.CreateRevokeRequest(alpaconClient, request)
		if err != nil {
			return utils.NewExitError("Failed to create revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request created: %s", response.ID)
		return nil
	}),
}

func init() {
	var certificate, requestedReason string
	var reason int
	revokeCreateCmd.Flags().StringVar(&certificate, "certificate", "", "Certificate ID to revoke")
	revokeCreateCmd.Flags().IntVar(&reason, "reason", 0, "Revocation reason code (0-10)")
	revokeCreateCmd.Flags().StringVar(&requestedReason, "requested-reason", "", "Detailed reason description")
}
