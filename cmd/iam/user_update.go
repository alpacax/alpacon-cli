package iam

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userUpdateCmd = &cobra.Command{
	Use:   "update USER",
	Short: "Update the user information",
	Long: `Open a user's details in your editor and save the fields you changed.

Only the fields you actually edit are sent, and the response is printed so you can
confirm what the server kept—some fields are read-only, and some need privileges
your account may not have.

is_staff and is_superuser are read-only projections of the user's RBAC roles.
Editing them here does nothing, so this command refuses to send them and tells you
the 'alpacon user role' command that makes the change instead. An edit that touched
one of them never exits 0, whether or not the rest of the edit was applied.`,
	Example: `  alpacon user update john`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		userName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		userID, edit, err := iam.PrepareUserUpdate(alpaconClient, userName)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the user info: %s.", err)
		}

		// Report the privilege edits before anything is sent, so the guidance is on
		// screen whether or not the rest of the patch succeeds.
		for _, privilege := range edit.Privileges {
			utils.CliWarning("%s is a read-only projection of %s's RBAC roles and was not sent.",
				privilege.Field, userName)
			utils.CliInfo("To make that change: %s", roleCommandFor(userName, privilege).PlainText())
		}

		if len(edit.Changes) == 0 {
			if len(edit.Privileges) == 0 {
				utils.CliInfoWithExit("No changes made. Aborting update.")
			}
			// Nothing else was edited, so there is nothing to apply and the operator's
			// stated intent was not met. Reporting success here is the bug this fixes.
			utils.CliErrorWithExit("Nothing was applied: the only edits were to read-only privilege flags.")
		}

		userDetail, err := iam.PatchUser(alpaconClient, userID, edit.Changes)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the user info: %s.", err)
		}

		utils.PrintJson(userDetail)

		// Part of what the operator typed was not applied, so this is not a success.
		// Exiting 0 here would be the same false green this command exists to remove,
		// only narrower: a wrapper reading the status would carry on as though the
		// promotion had landed.
		if len(edit.Privileges) > 0 {
			utils.CliErrorWithExit("The other edits were applied, but the privilege flags were not sent. Run the 'alpacon user role' command above to change them.")
		}

		utils.CliSuccess("User updated: %s", userName)
	},
}

// roleCommandFor is the 'alpacon user role' command that performs what an edit to a
// privilege flag was asking for. The mapping is the server's: the admin role is what
// is_staff projects, and the superuser role is what is_superuser projects.
//
// Clearing is_superuser maps to a plain revoke rather than --cascade, because the
// flag edit asked to stop being a superuser and nothing more; the companion admin
// binding is exactly what the CLI would keep.
func roleCommandFor(userName string, privilege iam.PrivilegeEdit) utils.NextAction {
	role := rbac.RoleAdmin
	if privilege.Field == "is_superuser" {
		role = rbac.RoleSuperuser
	}

	verb := "revoke"
	if privilege.Want {
		verb = "grant"
	}

	return utils.NextAction{Command: fmt.Sprintf("alpacon user role %s %s %s", verb, userName, role)}
}
