package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceMFAMethodsCmd = &cobra.Command{
	Use:     "mfa-methods",
	Aliases: []string{"mfa"},
	Short:   "Retrieve allowed MFA methods for the workspace",
	Long:    "Display the MFA methods available for the workspace including allowed methods and passkey-as-MFA setting.",
	Example: `
	alpacon workspace mfa-methods
	alpacon ws mfa`,
	RunE: func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		mfaMethodsDetail, err := workspace.GetMFAMethods(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve MFA methods: %s.", err)
		}

		utils.PrintJson(mfaMethodsDetail)
		return nil
	},
}
