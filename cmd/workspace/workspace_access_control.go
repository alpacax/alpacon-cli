package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newWorkspaceAccessControlCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "access-control",
		Aliases: []string{"acl"},
		Short:   "Retrieve workspace access control settings",
		Long:    "Display the current workspace access control settings including sudo, tunneling, and directory permissions.",
		Example: `
	alpacon workspace access-control
	alpacon ws acl`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			accessControlDetail, err := workspace.GetAccessControl(alpaconClient)
			if err != nil {
				return utils.NewExitError("Failed to retrieve access control settings: %s.", err)
			}

			return ios.PrintJson(accessControlDetail)
		}),
	}
	cmd.AddCommand(newWorkspaceAccessControlUpdateCmd(newClient, ios))
	return cmd
}
