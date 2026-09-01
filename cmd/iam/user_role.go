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
	Short: "Manage the workspace roles a user holds",
	Long: `Grant and revoke the workspace RBAC roles a user holds.

A role binding is immutable—the server has no update path, so changing a user's
roles means revoking one binding and granting another. Bindings created here are
workspace-wide, and only workspace-wide bindings decide whether someone is an
admin or a superuser. Object-scoped bindings such as server:owner appear in
'alpacon user role ls' but are managed by the owning resource's own command.

Granting 'superuser' also creates a companion 'admin' binding. Revoking
'superuser' leaves that companion in place—pass --cascade to remove both,
superuser first. Revoking 'admin' from someone who still holds 'superuser' is
refused before anything is sent: it would delete the binding and leave both
platform flags set.

These are not group membership roles. 'alpacon group member add --role' sets a
member's tier within a group (owner, manager, member), a different axis from a
workspace RBAC role.

Changing a role binding requires a workspace superuser and recent multi-factor
authentication. On self-hosted workspaces an API token is accepted and skips the
MFA step. On Alpacon Cloud workspaces an API token is refused on these commands,
reads included, so run 'alpacon login' first.

'history' is the one exception. It reads the audit log, which sits outside the
gate that refuses tokens elsewhere and asks instead for a token carrying the
role_audit_log:read scope, on either deployment.`,
	Example: `  alpacon user role ls john
  alpacon user role catalog
  alpacon user role describe superuser
  alpacon user role grant john operator --reason "on-call rotation Q3"
  alpacon user role revoke john superuser --cascade
  alpacon user role history john`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := cmd.Help()
		if err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon user role ls', 'alpacon user role catalog', 'alpacon user role describe', 'alpacon user role grant', 'alpacon user role revoke', or 'alpacon user role history'. Run 'alpacon user role --help' for more information")
	},
}

func init() {
	userRoleCmd.AddCommand(userRoleListCmd)
	userRoleCmd.AddCommand(userRoleCatalogCmd)
	userRoleCmd.AddCommand(userRoleDescribeCmd)
	userRoleCmd.AddCommand(userRoleGrantCmd)
	userRoleCmd.AddCommand(userRoleRevokeCmd)
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
