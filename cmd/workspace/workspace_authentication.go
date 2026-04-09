package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceAuthenticationCmd = &cobra.Command{
	Use:     "authentication",
	Aliases: []string{"auth"},
	Short:   "Retrieve workspace authentication settings",
	Long:    "Display the current workspace authentication settings including MFA requirements, timeout, and allowed methods.",
	Example: `
	alpacon workspace authentication
	alpacon ws auth`,
	RunE: func(cmd *cobra.Command, args []string) error {
		isSaaS, err := config.IsSaaS()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
		}
		if !isSaaS {
			utils.CliErrorWithExit("This command is only available on Alpacon Cloud workspaces.")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		authenticationDetail, err := workspace.GetAuthentication(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve authentication settings: %s.", err)
		}

		utils.PrintJson(authenticationDetail)
		return nil
	},
}

func init() {
	workspaceAuthenticationCmd.AddCommand(workspaceAuthenticationUpdateCmd)
}
