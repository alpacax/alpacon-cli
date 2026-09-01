package iam

import (
	"errors"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userRoleCmd = &cobra.Command{
	Use:   "role",
	Short: "Inspect the workspace roles a user holds",
	Long: `Read the workspace RBAC roles a user holds.

A binding is either workspace-wide or scoped to a single object, and only the
workspace-wide ones decide whether someone is an admin or a superuser. Object
ownership such as server:owner appears here but is managed by the resource that
owns it.

These are not group membership roles. 'alpacon group member add --role' sets a
member's tier within a group (owner, manager, member), a different axis from a
workspace RBAC role.

On Alpacon Cloud workspaces every RBAC request requires an interactive browser
login; run 'alpacon login' first.`,
	Example: `  alpacon user role ls john
  alpacon user role catalog
  alpacon user role describe superuser
  alpacon user role history john`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon user role ls', 'alpacon user role catalog', 'alpacon user role describe', or 'alpacon user role history'. Run 'alpacon user role --help' for more information")
	},
}

func init() {
	userRoleCmd.AddCommand(userRoleListCmd)
	userRoleCmd.AddCommand(userRoleCatalogCmd)
	userRoleCmd.AddCommand(userRoleDescribeCmd)
	userRoleCmd.AddCommand(userRoleHistoryCmd)
}

// resolveSubject turns an optional USER argument into a user id and the label to
// print alongside it. With no argument the subject is the caller, so 'alpacon user
// role ls' answers "what do I hold" without the operator knowing their own
// username—the same shape 'kubectl auth can-i' takes.
func resolveSubject(ac *client.AlpaconClient, args []string) (string, string) {
	if len(args) == 0 {
		current, err := iam.GetCurrentUser(ac)
		if err != nil {
			utils.CliErrorWithExit("Failed to identify the current user: %s.", err)
		}
		return current.ID, current.Username
	}

	if utils.IsUUID(args[0]) {
		return args[0], args[0]
	}

	userID, err := iam.GetUserIDByName(ac, args[0])
	if err != nil {
		utils.CliErrorWithExit("Failed to resolve the user %q: %s.", args[0], err)
	}

	return userID, args[0]
}
