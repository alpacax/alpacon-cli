package workspace

import (
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workspaceAccessControlCmd = &cobra.Command{
	Use:     "access-control",
	Aliases: []string{"acl"},
	Short:   "Retrieve workspace access control settings",
	Long:    "Display the current workspace access control settings including sudo, tunneling, and directory permissions.",
	Example: `
	alpacon workspace access-control
	alpacon ws acl`,
	RunE: func(cmd *cobra.Command, args []string) error {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		accessControlDetail, err := workspace.GetAccessControl(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve access control settings: %s.", err)
		}

		utils.PrintJson(accessControlDetail)
		return nil
	},
}

func init() {
	workspaceAccessControlCmd.AddCommand(workspaceAccessControlUpdateCmd)
}
