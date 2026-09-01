package iam

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userRoleRevokeCmd = &cobra.Command{
	Use:   "revoke USER ROLE",
	Short: "Revoke a workspace role from a user",
	Long: `Remove a user's workspace-wide binding to an RBAC role.

Revoking a role the user does not hold changes nothing and succeeds, and that
outranks everything below: a revoke with no binding to delete is never refused.

Revoking 'superuser' demotes the user to admin: the companion 'admin' binding the
superuser grant created stays, and so does workspace administrator access. Pass
--cascade to remove both, superuser first, which is the order that never leaves a
half-demoted account holding more than it should. With no superuser binding to
start from, --cascade revokes nothing rather than reaching for the companion.

Revoking an 'admin' binding from someone who still holds 'superuser' is refused
before anything is sent. The server would accept it and delete the binding, but
the account would keep every platform flag while no longer registering as an
admin. Revoke 'superuser' first, or pass --cascade to that command.`,
	Example: `  alpacon user role revoke john operator
  alpacon user role revoke john superuser --cascade --reason "rotated off on-call"
  alpacon user role revoke john admin --dry-run`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		reason := reasonFlag(cmd)
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")
		cascade, _ := cmd.Flags().GetBool("cascade")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		role := resolveRoleForBinding(alpaconClient, "revoke", args[0], args[1])
		subj := resolveSubject(alpaconClient, args[:1])

		if cascade && role.Name != rbac.RoleSuperuser {
			utils.CliErrorWithExit("--cascade only applies to the superuser role, which is the only grant that creates a second binding.")
		}

		bindings, err := rbac.GetUserBindings(alpaconClient, subj.ID)
		if err != nil {
			utils.CliErrorWithExit("Failed to read %s's current roles: %s.", subj.Label, describeRBACError(alpaconClient, gateRoleRead, err))
		}

		// Nothing-to-do is checked before the invariant guard: revoking an unheld role must succeed.
		targets := plannedRevocations(bindings, role.Name, cascade)
		if len(targets) == 0 {
			utils.CliInfo("%s does not hold %s workspace-wide. Nothing to do.", subj.Label, role.Name)
			return
		}

		if wouldStrandThePlatformFlags(bindings, role.Name, targets) {
			next := utils.NextAction{Command: fmt.Sprintf("alpacon user role revoke %s superuser --cascade", args[0])}
			utils.CliErrorWithExit("%s still holds the superuser role, and superuser implies admin. Revoking admin alone would delete the binding and leave every platform flag set. Revoke superuser first: %s",
				subj.Label, next.PlainText())
		}

		// Same ordering as grant: what --dry-run is for is seeing these before the write.
		if rbac.IsPlatformTier(role.Name) {
			if role.Name == rbac.RoleSuperuser && !cascade && rbac.HoldsWorkspaceRole(bindings, rbac.RoleAdmin) {
				utils.CliInfo("The companion admin binding stays, so %s remains a workspace administrator; --cascade removes both.", subj.Label)
			}
			warnMissingReason(reason)
		}

		if dryRun {
			for _, target := range targets {
				utils.CliInfo("Would revoke %s from %s (binding %s).", target.Role.Name, subj.Label, target.ID)
			}
			return
		}

		if rbac.IsPlatformTier(role.Name) && !yes {
			utils.ConfirmAction("Revoke %s from %s?", describeTargets(targets), subj.Label)
		}

		// Superuser first: the reverse order fails open, leaving superuser standing once admin is gone.
		for index, target := range targets {
			revoke := func() error {
				return rbac.RevokeRole(alpaconClient, target.ID, reason)
			}

			if err = revoke(); err != nil {
				err = utils.HandleCommonErrors(err, "", mfa.WorkspaceErrorCallbacks(alpaconClient, revoke))
			}
			if err != nil {
				if index > 0 {
					resume := utils.NextAction{Command: fmt.Sprintf("alpacon user role revoke %s %s", args[0], target.Role.Name)}
					utils.CliWarning("Revoked %s, but %s was left in place.", targets[index-1].Role.Name, target.Role.Name)
					utils.CliInfo("To finish: %s", resume.PlainText())
				}
				utils.CliErrorWithExit("Failed to revoke %s from %s: %s.", target.Role.Name, subj.Label, describeRBACError(alpaconClient, gateRoleWrite, err))
			}
		}

		utils.CliSuccess("Revoked %s from %s.", describeTargets(targets), subj.Label)
		if role.Name == rbac.RoleSuperuser && !cascade && rbac.HoldsWorkspaceRole(bindings, rbac.RoleAdmin) {
			next := utils.NextAction{Command: fmt.Sprintf("alpacon user role revoke %s admin", args[0])}
			utils.CliInfo("The companion admin binding remains, so %s is still a workspace administrator. To remove it too: %s", subj.Label, next.PlainText())
		}
		reportWorkspaceRoles(alpaconClient, subj.ID, subj.Label)
	},
}

func init() {
	userRoleRevokeCmd.Flags().Bool("cascade", false, "For superuser: also revoke the companion admin binding, superuser first. Nothing is revoked if the user holds no superuser binding")
	userRoleRevokeCmd.Flags().String("reason", "", "Justification recorded on the change, readable with 'alpacon user role history'")
	userRoleRevokeCmd.Flags().Bool("dry-run", false, "Print what would be deleted and exit without writing")
	userRoleRevokeCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
}

// plannedRevocations lists the bindings to delete, in delete order.
//
// The companion is planned only when the named binding was found: an admin-only user is
// indistinguishable from a cascade interrupted after the first delete, so treating that
// state as a resume would let 'revoke USER superuser --cascade' strip admin from someone
// who was never a superuser. Resuming stays explicit via 'revoke USER admin'.
func plannedRevocations(bindings []rbac.UserRoleResponse, roleName string, cascade bool) []rbac.UserRoleResponse {
	target := rbac.FindWorkspaceBindingByName(bindings, roleName)
	if target == nil {
		return nil
	}

	targets := []rbac.UserRoleResponse{*target}
	if cascade {
		if companion := rbac.FindWorkspaceBindingByName(bindings, rbac.RoleAdmin); companion != nil {
			targets = append(targets, *companion)
		}
	}

	return targets
}

// The admin-while-superuser case: the server accepts the delete, then re-forces is_staff
// because is_superuser still stands, so both platform flags survive while the account
// stops registering as an admin.
func wouldStrandThePlatformFlags(bindings []rbac.UserRoleResponse, roleName string, targets []rbac.UserRoleResponse) bool {
	return roleName == rbac.RoleAdmin &&
		len(targets) > 0 &&
		rbac.HoldsWorkspaceRole(bindings, rbac.RoleSuperuser)
}

func describeTargets(targets []rbac.UserRoleResponse) string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.Role.Name)
	}

	return strings.Join(names, " and ")
}
