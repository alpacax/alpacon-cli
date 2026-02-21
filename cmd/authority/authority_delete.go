package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDeleteCmd = &cobra.Command{
	Use:     "delete AUTHORITY",
	Aliases: []string{"rm"},
	Short:   "Delete a CA along with its certificate and CSR",
	Long: `
    This command removes a Certificate Authority (CA) from the system, including its certificate and CSR.
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
			utils.ConfirmAction("Delete CA '%s'? This will also remove its certificate and CSR.", authorityName)
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
			utils.CliErrorWithExit("Failed to delete the CA: %s.", err)
		}

		utils.CliSuccess("CA deleted: %s", authorityName)
	},
}

func init() {
	authorityDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
