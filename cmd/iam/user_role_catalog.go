package iam

import (
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userRoleCatalogCmd = &cobra.Command{
	Use:     "catalog",
	Aliases: []string{"roles"},
	Short:   "List the workspace roles that exist",
	Long: `List the RBAC roles defined in this workspace.

Role names are matched exactly and are case-sensitive wherever a command takes
one, so this is the list to check a spelling against.

--hide-object-roles asks the server to drop the roles whose names end in :owner,
:master, :member or :manager. Those are the object-scoped plumbing roles created
by the resources that own them. It is a name-suffix filter and nothing more, so it
also hides service_token:manager, which is granted by hand, and it keeps member,
which every account holds.`,
	Example: `  alpacon user role catalog
  alpacon user role catalog --hide-object-roles
  alpacon user role catalog --output json`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		// Send the filter only to hide. An explicit --hide-object-roles=false asks for
		// the whole catalog, not for auto_assigned=true, which would return nothing but
		// the object-scoped roles.
		var autoAssigned *bool
		if hide, _ := cmd.Flags().GetBool("hide-object-roles"); hide {
			value := false
			autoAssigned = &value
		}

		roles, err := rbac.GetRoleCatalog(alpaconClient, autoAssigned)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the role catalog: %s.", describeRBACError(alpaconClient, err))
		}

		utils.PrintTable(rbac.RoleAttributesFrom(roles))
	},
}

func init() {
	userRoleCatalogCmd.Flags().Bool("hide-object-roles", false, "Hide roles whose names end in :owner, :master, :member or :manager")
}
