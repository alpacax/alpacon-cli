package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newWorkspaceAuthenticationCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "authentication",
		Aliases: []string{"auth"},
		Short:   "Retrieve workspace authentication settings",
		Long:    "Display the current workspace authentication settings including MFA requirements, timeout, and allowed methods.",
		Example: `
	alpacon workspace authentication
	alpacon ws auth`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			isSaaS, err := config.IsSaaS()
			if err != nil {
				return utils.NewExitError("Not logged in. Run 'alpacon login' first.")
			}
			if !isSaaS {
				return utils.NewExitError("This command is only available on Alpacon Cloud workspaces.")
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			authenticationDetail, err := workspace.GetAuthentication(alpaconClient)
			if err != nil {
				return utils.NewExitError("Failed to retrieve authentication settings: %s.", err)
			}

			return ios.PrintJson(authenticationDetail)
		}),
	}
	cmd.AddCommand(newWorkspaceAuthenticationUpdateCmd(newClient, ios))
	return cmd
}
