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

--hide-object-roles asks the server to leave out the roles it counts as assigned
on its own, as a consequence of creating a resource or joining a group, rather
than picked from this list. user:owner, which each account holds over its own
user record, and group:member are two of them. So are per-resource roles such as
server:maintainer and the older server:owner; the catalog has one or both,
depending on the Alpacon release.

'member' is not one of them and stays in the list, though every account holds
it. On an older Alpacon release the server may match on the name alone and hide
every role ending in :owner, :master, :member or :manager, including ones granted
by hand such as service_token:manager. If a role you expected is missing, drop
the flag and read the whole catalog.`,
	Example: `  alpacon user role catalog
  alpacon user role catalog --hide-object-roles
  alpacon user role catalog --output json`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		// Filter only when hiding: --hide-object-roles=false asks for the whole catalog, and
		// auto_assigned=true would return nothing but the roles the flag hides.
		var autoAssigned *bool
		if hide, _ := cmd.Flags().GetBool("hide-object-roles"); hide {
			value := false
			autoAssigned = &value
		}

		roles, err := rbac.GetRoleCatalog(alpaconClient, autoAssigned)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the role catalog: %s.", describeRBACError(alpaconClient, gateRoleRead, err))
		}

		utils.PrintTable(rbac.RoleAttributesFrom(roles))
	},
}

func init() {
	// The flag keeps its name for the scripts that already pass it, although what it hides
	// is the server's answer now rather than a spelling.
	userRoleCatalogCmd.Flags().Bool("hide-object-roles", false, "Hide the roles the server counts as assigned on its own, such as group:member or user:owner")
}
