package iam

import (
	"strings"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userRoleGrantCmd = &cobra.Command{
	Use:   "grant USER ROLE",
	Short: "Grant a workspace role to a user",
	Long: `Bind a workspace RBAC role to a user.

The binding is workspace-wide. Granting 'admin' is what makes someone a workspace
administrator, and granting 'superuser' is what makes someone a platform
operator—the server creates the companion 'admin' binding for you, so grant
'superuser' alone rather than both.

Re-running a grant the user already holds changes nothing and succeeds, so this is
safe to put in a script that cannot know the current state.

The role name is matched exactly and is case-sensitive. Run 'alpacon user role
catalog' to see the names this workspace defines.`,
	Example: `  alpacon user role grant john operator --reason "on-call rotation Q3"
  alpacon user role grant john superuser --reason "SEC-1421" -y
  alpacon user role grant john auditor --dry-run`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		reason := reasonFlag(cmd)
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		// The role is resolved first so a transposed command line is caught as such:
		// resolving the user first would fail on the role name and never reach the hint.
		role := resolveRoleForBinding(alpaconClient, "grant", args[0], args[1])
		userID, userLabel := resolveSubject(alpaconClient, args[:1])

		bindings, err := rbac.GetUserBindings(alpaconClient, userID)
		if err != nil {
			utils.CliErrorWithExit("Failed to read %s's current roles: %s.", userLabel, describeRBACError(alpaconClient, gateRoleRead, err))
		}

		if rbac.FindWorkspaceBinding(bindings, role.ID) != nil {
			utils.CliInfo("%s already holds %s workspace-wide. Nothing to do.", userLabel, role.Name)
			return
		}

		request := rbac.BindingCreateRequest{User: userID, Role: role.ID, Reason: reason}

		if dryRun {
			utils.CliInfo("Would grant %s to %s (user %s, role %s), workspace-wide.", role.Name, userLabel, userID, role.ID)
			return
		}

		if rbac.IsPlatformTier(role.Name) {
			warnMissingReason(reason)
			if !yes {
				if role.Name == rbac.RoleSuperuser {
					utils.ConfirmAction("Grant %s the superuser role? The server also creates a companion workspace-wide admin binding.", userLabel)
				} else {
					utils.ConfirmAction("Grant %s the %s role?", userLabel, role.Name)
				}
			}
		}

		grant := func() error {
			_, grantErr := rbac.GrantRole(alpaconClient, request)
			return grantErr
		}

		if err = grant(); err != nil {
			err = utils.HandleCommonErrors(err, "", mfa.WorkspaceErrorCallbacks(alpaconClient, grant))
		}
		// A duplicate means another writer got there first. The end state is the one
		// asked for, so report it as reached rather than as a failure.
		if err != nil && !rbac.IsDuplicateBinding(err) {
			utils.CliErrorWithExit("Failed to grant %s to %s: %s.", role.Name, userLabel, describeRBACError(alpaconClient, gateRoleWrite, err))
		}

		utils.CliSuccess("Granted %s to %s.", role.Name, userLabel)
		reportWorkspaceRoles(alpaconClient, userID, userLabel)
	},
}

func init() {
	userRoleGrantCmd.Flags().String("reason", "", "Justification recorded on the change, readable with 'alpacon user role history'")
	userRoleGrantCmd.Flags().Bool("dry-run", false, "Print what would be sent and exit without writing")
	userRoleGrantCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
}

// resolveRoleForBinding resolves the ROLE argument, and on failure checks whether
// the two arguments were typed the other way round before giving up. Most CLIs in
// this space put the role first, so 'grant admin john' is the mistake an operator
// arriving from one of them makes.
func resolveRoleForBinding(ac *client.AlpaconClient, verb, userArg, roleArg string) *rbac.RoleResponse {
	role, err := rbac.ResolveRole(ac, roleArg)
	if err == nil {
		return role
	}

	if _, swapErr := rbac.ResolveRole(ac, userArg); swapErr == nil {
		utils.CliErrorWithExit("No role named %q, but %q is one. The user comes first: 'alpacon user role %s %s %s'.",
			roleArg, userArg, verb, roleArg, userArg)
	}

	utils.CliErrorWithExit("Failed to resolve the role: %s.", describeRBACError(ac, gateRoleRead, err))
	return nil
}

// reasonFlag reads --reason and trims it once, so the value the warning judges is the
// value the server records. Sending a whitespace-only reason would fill the audit
// entry with blanks while the server's own unjustified-grant warning stayed quiet,
// which is worse than the honest omission the operator meant.
func reasonFlag(cmd *cobra.Command) string {
	reason, _ := cmd.Flags().GetString("reason")

	return strings.TrimSpace(reason)
}

// warnMissingReason says what an unjustified platform-tier change costs. The server
// accepts a blank justification and records the omission; it does not refuse.
func warnMissingReason(reason string) {
	if reason == "" {
		utils.CliWarning("No --reason given, so the audit entry for this change will carry no justification.")
	}
}

// reportWorkspaceRoles re-reads the user's bindings and prints what they now hold.
// A 2xx is not evidence on its own here: the superuser grant creates a second
// binding the client never asked for, and the revoke path can leave one behind.
func reportWorkspaceRoles(ac *client.AlpaconClient, userID, userLabel string) {
	bindings, err := rbac.GetUserBindings(ac, userID)
	if err != nil {
		utils.CliWarning("The change was accepted, but re-reading %s's roles failed: %s.", userLabel, describeRBACError(ac, gateRoleRead, err))
		return
	}

	names := rbac.WorkspaceRoleNames(bindings)
	if len(names) == 0 {
		utils.CliInfo("%s now holds no workspace-wide roles.", userLabel)
		return
	}

	utils.CliInfo("%s now holds workspace-wide: %s", userLabel, strings.Join(names, ", "))
}
