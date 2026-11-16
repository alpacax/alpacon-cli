package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		isSaaS, err := config.IsSaaS()
		if err != nil {
			return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
		}
		if !isSaaS {
			return utils.NewExitError("This command is only available on Alpacon Cloud workspaces.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		mfaMethodsDetail, err := workspace.GetMFAMethods(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve MFA methods: %s.", err)
		}

		return utils.PrintJson(mfaMethodsDetail)
	}),
}
