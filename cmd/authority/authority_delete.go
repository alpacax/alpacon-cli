package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDeleteCmd = &cobra.Command{
	Use:     "delete AUTHORITY_ID",
	Aliases: []string{"rm"},
	Short:   "Delete a CA along with its certificate and CSR",
	Long: `
    This command removes a Certificate Authority (CA) from the system, including its certificate and CSR. 
	Note that this action requires manual configuration adjustments to alpamon-cert-authority.
	`,
	Example: `
	alpacon authority delete 550e8400-e29b-41d4-a716-446655440000
	alpacon authority rm 550e8400-e29b-41d4-a716-446655440000
	alpacon authority delete 550e8400-e29b-41d4-a716-446655440000 -y
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		authorityId := args[0]

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			utils.ConfirmAction("Delete CA '%s'? This will also remove its certificate and CSR.", authorityId)
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.DeleteCA(alpaconClient, authorityId)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the CA: %s.", err)
		}

		utils.CliSuccess("CA deleted: %s", authorityId)
	},
}

func init() {
	authorityDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}
