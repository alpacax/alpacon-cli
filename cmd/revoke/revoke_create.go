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
	alpacon revoke create --certificate=CERT_ID --reason=1 --requested-reason="Key was compromised"
	`,
	Run: func(cmd *cobra.Command, args []string) {
		certificate, _ := cmd.Flags().GetString("certificate")
		reason, _ := cmd.Flags().GetInt("reason")
		requestedReason, _ := cmd.Flags().GetString("requested-reason")

		if certificate == "" {
			certificate = utils.PromptForRequiredInput("Certificate ID: ")
		}
		if !cmd.Flags().Changed("reason") {
			reason = utils.PromptForRequiredIntInput("Reason code (0=Unspecified, 1=Key Compromise, 2=CA Compromise, 3=Affiliation Changed, 4=Superseded, 5=Cessation Of Operation): ")
		}
		if requestedReason == "" {
			requestedReason = utils.PromptForInput("Requested reason (description, optional): ")
		}

		request := cert.RevokeRequestCreate{
			Certificate:     certificate,
			Reason:          reason,
			RequestedReason: requestedReason,
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		response, err := cert.CreateRevokeRequest(alpaconClient, request)
		if err != nil {
			utils.CliErrorWithExit("Failed to create revoke request: %s.", err)
		}

		utils.CliSuccess("Revoke request created: %s", response.ID)
	},
}

func init() {
	var certificate, requestedReason string
	var reason int
	revokeCreateCmd.Flags().StringVar(&certificate, "certificate", "", "Certificate ID to revoke")
	revokeCreateCmd.Flags().IntVar(&reason, "reason", 0, "Revocation reason code (0-10)")
	revokeCreateCmd.Flags().StringVar(&requestedReason, "requested-reason", "", "Detailed reason description")
}
