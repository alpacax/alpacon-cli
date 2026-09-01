package iam

import (
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userRoleListCmd = &cobra.Command{
	Use:     "ls [USER]",
	Aliases: []string{"list"},
	Short:   "List the role bindings a user holds",
	Long: `List the RBAC role bindings a user holds, at every scope tier.

With no USER the subject is you. The SCOPE column reads 'workspace' for the
workspace-wide bindings that decide admin and superuser status; anything else is
an object-scoped binding managed by the resource that owns it.

Reading another user's bindings needs a workspace admin. Without that the server
narrows the answer to your own rather than refusing, so an empty table can mean
"not visible to you" rather than "holds nothing".`,
	Example: `  alpacon user role ls
  alpacon user role ls john
  alpacon user role ls john --output json`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		userID, _ := resolveSubject(alpaconClient, args)

		bindings, err := rbac.GetUserBindings(alpaconClient, userID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the role bindings: %s.", describeRBACError(alpaconClient, err))
		}

		utils.PrintTable(rbac.BindingAttributesFrom(bindings))
	},
}
