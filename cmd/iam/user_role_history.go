package iam

import (
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userRoleHistoryCmd = &cobra.Command{
	Use:   "history [USER]",
	Short: "Show who changed a user's roles, and why",
	Long: `Show the RBAC role changes recorded against a user, newest first.

This is where the justification passed to --reason and the identity of whoever
made the change are kept; a role binding itself records neither. With no USER the
subject is you.

This lists the changes recorded against USER, so a grant you made to someone else
appears in their history rather than yours. What you can read also depends on your
account: an auditor of the whole workspace sees every change within the plan's
audit window, and everyone else sees only rows that name them.

This is the one 'alpacon user role' command an API token can reach on an Alpacon
Cloud workspace. The audit log sits outside the gate that refuses tokens on the
other RBAC routes, and asks instead for a token carrying the role_audit_log:read
scope.`,
	Example: `  alpacon user role history
  alpacon user role history john
  alpacon user role history john --tail 100`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tail, _ := cmd.Flags().GetInt("tail")
		utils.RequirePositiveInt("tail", tail)

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		subj := resolveSubject(alpaconClient, args)

		entries, err := rbac.GetRoleHistory(alpaconClient, subj.ID, tail)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the role history: %s.", describeRBACError(alpaconClient, gateAuditRead, err))
		}

		utils.PrintTable(rbac.AuditAttributesFrom(entries))
	},
}

func init() {
	userRoleHistoryCmd.Flags().IntP("tail", "t", 25, "Number of entries to show, newest first")
}
