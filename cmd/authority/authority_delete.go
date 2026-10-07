package authority

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDeleteCmd = &cobra.Command{
	Use:     "delete AUTHORITY",
	Aliases: []string{"rm"},
	Short:   "Delete a CA that has no certificate history",
	Long: `
	This command removes a Certificate Authority (CA) that has never received a certificate sign request.
	A CA that has received one cannot be deleted: the server refuses with cert_authority_cannot_be_deleted
	and keeps the CA along with its sign requests, issued certificates and revoke requests.
	To stop using such a CA, delete the server it runs on ('alpacon server delete'). The server has to be
	disconnected first, since a connected server cannot be deleted, and this removes the server itself,
	not just the CA. The CA then leaves the CA list and signs no new requests, while its records are kept.
	Note that this action requires manual configuration adjustments to alpamon-cert-authority.
	`,
	Example: `
	alpacon authority delete "Root CA"
	alpacon authority rm my-authority
	alpacon authority delete my-authority -y
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		authorityName := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete CA '%s'? A CA that has received certificate requests cannot be deleted; "+
				"to stop using one, disconnect the server it runs on and delete it with 'alpacon server delete' "+
				"(this removes the server itself).", authorityName)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authorityID, err := cert.GetAuthorityIDByName(alpaconClient, authorityName)
		if err != nil {
			utils.CliErrorWithExit("Failed to find authority: %s.", err)
		}

		err = cert.DeleteCA(alpaconClient, authorityID)
		if err != nil {
			utils.CliErrorWithExit("%s", deleteCAErrorText(authorityName, err))
		}

		utils.CliSuccess("CA deleted: %s", authorityName)
	},
}

// deleteCAErrorText maps a DeleteCA error to the message shown to the user.
func deleteCAErrorText(authorityName string, err error) string {
	if code, _ := utils.ParseErrorResponse(err); code == cert.AuthorityCannotBeDeleted {
		return fmt.Sprintf("CA '%s' has received certificate requests, so it cannot be deleted; "+
			"its records are kept. To stop using it, disconnect the server it runs on and delete it "+
			"with 'alpacon server delete' (this removes the server itself).", authorityName)
	}
	return fmt.Sprintf("Failed to delete the CA: %s.", err)
}

func init() {
	authorityDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
