package iam

import (
	"os"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userRoleDescribeCmd = &cobra.Command{
	Use:     "describe ROLE",
	Aliases: []string{"desc"},
	Short:   "Show what a role grants and who holds it",
	Long: `Show the permissions a workspace role carries and the users bound to it.

The role is matched by exact, case-sensitive name, or by UUID. A role you cannot
see reads the same as one that does not exist, because the server narrows the
catalog rather than refusing—run 'alpacon user role catalog' to see what is
visible to you.

Holders are listed at every scope tier, so an object-scoped binding of the same
role appears alongside the workspace-wide ones that decide admin and superuser
status. Only the holders your account may see are listed—a workspace admin sees
them all, and everyone else sees just their own binding, with nothing to say the
list was narrowed.`,
	Example: `  alpacon user role describe operator
  alpacon user role describe superuser
  alpacon user role describe security_admin --output json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		role, err := rbac.ResolveRole(alpaconClient, args[0])
		if err != nil {
			utils.CliErrorWithExit("Failed to resolve the role: %s.", describeRBACError(alpaconClient, err))
		}

		scopes, err := rbac.GetRoleScopes(alpaconClient, role.ID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the role's permissions: %s.", describeRBACError(alpaconClient, err))
		}

		holders, err := rbac.GetRoleHolders(alpaconClient, role.ID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the role's holders: %s.", describeRBACError(alpaconClient, err))
		}

		// One list request resolves every holder's username. The nested user object on
		// a binding carries a display name, which is not what 'user role grant' takes.
		var usernames map[string]string
		if noResolve, _ := cmd.Flags().GetBool("no-resolve-names"); !noResolve {
			usernames, err = iam.GetUsernamesByID(alpaconClient)
			if err != nil {
				utils.CliErrorWithExit("Failed to resolve the holders' usernames: %s.", err)
			}
		}

		permissionRows := rbac.ScopeAttributesFrom(scopes)
		holderRows := rbac.HolderAttributesFrom(holders, usernames)

		if utils.OutputFormat == utils.OutputFormatJSON {
			combined := map[string]any{
				"role":        role,
				"permissions": jsonSlice(permissionRows),
				"holders":     jsonSlice(holderRows),
			}
			if err = utils.PrintJSONValue(os.Stdout, combined); err != nil {
				utils.CliErrorWithExit("Failed to render the role: %s.", err)
			}
			return
		}

		utils.PrintHeader("Permissions")
		utils.PrintTable(permissionRows)
		utils.PrintHeader("Holders")
		utils.PrintTable(holderRows)
	},
}

func init() {
	userRoleDescribeCmd.Flags().Bool("no-resolve-names", false, "Print holder ids instead of usernames, skipping the user lookup")
}
