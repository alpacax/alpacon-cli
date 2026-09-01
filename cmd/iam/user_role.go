package iam

import (
	"errors"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// selfUserPK is the alias the IAM user routes take in place of a UUID to mean the
// requesting user. See the subject type for why the distinction matters.
const selfUserPK = "-"

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
superuser first. Revoking an 'admin' binding from someone who still holds
'superuser' is refused before anything is sent: it would delete the binding and
leave both platform flags set. Revoking a role a user does not hold is never
refused—it reports nothing to do.

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

// subject is who a command is acting on.
//
// PK and ID differ for the self form on purpose. The IAM user routes take "-" as
// "me", and UserViewSet.get_object short-circuits on it and returns the request user
// without running the object permission check. That check is the only thing standing
// between an operator and their own permissions: /api/iam/users/{id}/permissions/
// pins no scope of its own, so addressing it by UUID auto-resolves to an orphan
// 'user:permissions' that nothing short of the superuser wildcard grants, and
// /effective-permissions/ pins 'user:read', which the baseline member role does not
// carry. Addressed by UUID, both refuse a caller reading their own account.
//
// ID is the resolved UUID, for the endpoints that take the user in a query filter or
// a request body, where "-" is not a value the server accepts.
type subject struct {
	PK    string
	ID    string
	Label string
}

func init() {
	userRoleCmd.AddCommand(userRoleListCmd)
	userRoleCmd.AddCommand(userRoleCatalogCmd)
	userRoleCmd.AddCommand(userRoleDescribeCmd)
	userRoleCmd.AddCommand(userRoleGrantCmd)
	userRoleCmd.AddCommand(userRoleRevokeCmd)
	userRoleCmd.AddCommand(userRoleHistoryCmd)
}

// resolveSubject turns an optional USER argument into the subject to act on. With no
// argument the subject is the caller, so 'alpacon user role ls' answers "what do I
// hold" without the operator knowing their own username—the same shape 'kubectl auth
// can-i' takes.
func resolveSubject(ac *client.AlpaconClient, args []string) subject {
	if len(args) == 0 {
		current, err := iam.GetCurrentUser(ac)
		if err != nil {
			utils.CliErrorWithExit("Failed to identify the current user: %s.", err)
		}
		return subject{PK: selfUserPK, ID: current.ID, Label: current.Username}
	}

	if utils.IsUUID(args[0]) {
		return subject{PK: args[0], ID: args[0], Label: args[0]}
	}

	userID, err := iam.GetUserIDByName(ac, args[0])
	if err != nil {
		utils.CliErrorWithExit("Failed to resolve the user %q: %s.", args[0], err)
	}

	return subject{PK: userID, ID: userID, Label: args[0]}
}
