package authority

import (
	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var authorityDeleteCmd = &cobra.Command{
	Use:     "delete [CSR ID]",
	Aliases: []string{"rm"},
	Short:   "Delete a CA along with its certificate and CSR",
	Long: `
    This command removes a Certificate Authority (CA) from the system, including its certificate and CSR. 
	Note that this action requires manual configuration adjustments to alpamon-cert-authority.
	`,
	Example: ` 
	alpacon authority delete [AUTHORITY ID]	
	alpacon authority rm [AUTHORITY_ID]
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		authorityId := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = cert.DeleteCA(alpaconClient, authorityId)
		if err != nil {
			utils.CliErrorWithExit("Failed to delete the CA: %s.", err)
		}

		utils.CliInfo("CA successfully deleted: %s.", authorityId)
	},
}
