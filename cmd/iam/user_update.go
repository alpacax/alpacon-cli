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

		// Report the privilege edits first, so the guidance survives a failed patch.
		for _, privilege := range edit.Privileges {
			utils.CliWarning("%s is a read-only projection of %s's RBAC roles and was not sent.",
				privilege.Field, userName)
			utils.CliInfo("To make that change: %s", roleCommandFor(userName, privilege).PlainText())
		}

		if len(edit.Changes) == 0 {
			if len(edit.Privileges) == 0 {
				utils.CliInfoWithExit("No changes made. Aborting update.")
			}
			utils.CliErrorWithExit("Nothing was applied: the only edits were to read-only privilege flags.")
		}

		userDetail, err := iam.PatchUser(alpaconClient, userID, edit.Changes)
		if err != nil {
			utils.CliErrorWithExit("Failed to update the user info: %s.", err)
		}

		utils.PrintJson(userDetail)

		// Non-zero exit: a wrapper reading 0 would carry on as though the promotion had landed.
		if len(edit.Privileges) > 0 {
			utils.CliErrorWithExit("The other edits were applied, but the privilege flags were not sent. Run the 'alpacon user role' command above to change them.")
		}

		utils.CliSuccess("User updated: %s", userName)
	},
}

// roleCommandFor maps a privilege-flag edit to the equivalent 'alpacon user role' command.
// The mapping is the server's: is_superuser is superuser, every other privilege flag admin.
// No --cascade on a revoke: the edit asked to drop superuser only, leaving the companion
// admin binding.
func roleCommandFor(userName string, privilege iam.PrivilegeEdit) utils.NextAction {
	role := rbac.RoleAdmin
	if privilege.Field == "is_superuser" {
		role = rbac.RoleSuperuser
	}

	verb := "revoke"
	if privilege.Enable {
		verb = "grant"
	}

	return utils.NextAction{Command: fmt.Sprintf("alpacon user role %s %s %s", verb, userName, role)}
}
