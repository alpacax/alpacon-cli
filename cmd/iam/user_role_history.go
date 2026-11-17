package iam

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newUserRoleHistoryCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history [USER]",
		Short: "Show who changed a user's roles, and why",
		Long: `Show the RBAC role changes recorded against a user, newest first.

This is where the justification passed to --reason and the identity of whoever
made the change are kept; a role binding itself records neither. With no USER the
subject is you.

This lists the changes recorded against USER, so a grant you made to someone else
appears in their history rather than yours. How much of that history you can read
depends on your account, and a reach line above the table says which applies (the
"reach" key under --output json):

  org    The whole workspace, within the plan's audit window. It takes a paid
         plan, the role_audit_log:read scope, and the admin role bound to you
         directly (superuser included). Nothing else widens it, and an auditor
         binding on the whole workspace does not.
  bound  On a paid plan, within the plan's audit window: USER's changes recorded
         while USER was a member of a group you hold auditor on through a binding
         made on that group itself. You need not belong to the group, and
         belonging to it is not enough. Auditor held through a group, bound on
         the whole workspace, or bound on every group at once does not count.
         Membership is read as it was when each change was recorded, and changes
         recorded before the server began noting it belong to no group.
         Holding application:maintainer directly on an Application also gives
         bound, but it adds nothing here: those changes name the Application,
         and this command lists one user's.
  self   Only the changes that name you, as the actor or the subject. Every other
         account gets this, and so does every reader for changes outside the two
         views above.

So with someone else as USER, an auditor gets USER's changes in a group they
audit along with the changes they made to USER themselves, or, when USER is in no
such group, only the changes that name both of them. Neither is an error, and the
rows may not name you at all. A change that names an object you do not reach
shows its kind and a hidden count under Target, and its reason as (withheld).

This is the one 'alpacon user role' command an API token can reach. The audit log
sits outside the gate that refuses tokens on the other RBAC routes, and asks
instead for a token carrying the role_audit_log:read scope. A personal token then
reads what its account reaches. A service token is let in but reads nothing, so
the command exits 1 saying so (error_code role_audit_reach_none), or fails earlier
without USER, since the token has no account of its own to default to.`,
		Example: `  alpacon user role history
  alpacon user role history john
  alpacon user role history john --tail 100
  alpacon user role history john --output json`,
		Args: cobra.MaximumNArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			tail, _ := cmd.Flags().GetInt("tail")
			if err := utils.CheckPositiveInt("tail", tail); err != nil {
				return err
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			subj, err := resolveSubject(alpaconClient, args)
			if err != nil {
				return err
			}

			entries, reach, err := rbac.GetRoleHistory(alpaconClient, subj.ID, tail)
			if err != nil {
				return utils.NewExitError("Failed to retrieve the role history: %s.", describeRBACError(alpaconClient, gateAuditRead, err))
			}

			// The server admits the credential to an empty answer by construction, so the list
			// says nothing about the user; reading it as "no changes" would be wrong.
			if reach.Stated() && reach.Value == rbac.ReachNone {
				return utils.NewExitErrorWithErrorCode(utils.ExitCodeGeneralError, errorCodeReachNone, "This credential may not read the role-change log (reach: none), so it says nothing about %s's roles. A service token gets this; run 'alpacon login' to read it as your account.", subj.Label)
			}
			if reach.Changed {
				ios.Warning(reachChangedWarning)
			}

			rows := rbac.AuditAttributesFrom(entries, time.Local)
			if ios.IsJSON() {
				return printRoleHistoryJSON(ios, reach, rows)
			}

			line := reachLine(reach)
			_, _ = fmt.Fprintln(ios.ErrOut, line)
			if err := ios.PrintTable(rows); err != nil {
				return err
			}
			repeatLine, repeatWarning := noticesToRepeat(reach, utils.LastOutputPaged())
			if repeatLine {
				_, _ = fmt.Fprintln(ios.ErrOut, line)
			}
			if repeatWarning {
				ios.Warning(reachChangedWarning)
			}
			return nil
		}),
	}

	cmd.Flags().IntP("tail", "t", 25, "Number of entries to show, newest first")

	return cmd
}

// errorCodeReachNone is the --output json error_code of a credential the log admits to an empty
// answer, so a script can tell it from any other failure.
const errorCodeReachNone = "role_audit_reach_none"

const reachChangedWarning = "The pages of the role history did not state the same reach, because your access or the server changed during the read, so the list may mix both."

// noticesToRepeat says what to print again below a table the pager showed, since the pager scrolls
// the lines above it away: the reach line unless it is org, and the warning that pages disagreed.
func noticesToRepeat(reach rbac.Reach, paged bool) (line, warning bool) {
	if !paged {
		return false, false
	}
	return !reach.Stated() || reach.Value != rbac.ReachOrg, reach.Changed
}

// roleHistoryOutput is the --output json document. Reach is the value as the server sent it,
// null when it sent none, so a script reads how far Entries reach from the data itself.
type roleHistoryOutput struct {
	Reach        json.RawMessage            `json:"reach"`
	ReachChanged bool                       `json:"reach_changed,omitempty"`
	Entries      []rbac.RoleAuditAttributes `json:"entries"`
}

func printRoleHistoryJSON(ios *utils.IOStreams, reach rbac.Reach, rows []rbac.RoleAuditAttributes) error {
	output := roleHistoryOutput{Reach: reach.Raw, ReachChanged: reach.Changed, Entries: jsonSlice(rows)}
	if err := utils.PrintJSONValue(ios.Out, output); err != nil {
		return utils.NewExitError("Failed to marshal data to JSON: %s", err)
	}

	return nil
}

// reachLine says how far the reader may read, in the server's word; only a known value may omit
// "may not be complete", and bound never claims an auditor binding, which a maintainer lacks.
func reachLine(reach rbac.Reach) string {
	if !reach.Stated() {
		return "The server did not say how far this list reaches, so it may not be complete."
	}

	switch reach.Value {
	case rbac.ReachOrg:
		return "Reach: org—you may read every role change in this workspace within the plan's audit window, and every change that names you."
	case rbac.ReachBound:
		return "Reach: bound—beyond the changes that name you, only changes recorded while their subject was in a group you audit through a binding on that group itself, within the plan's audit window. Rows outside that reach are not shown."
	case rbac.ReachSelf:
		return "Reach: self—only changes that name you, as the actor or the subject. Rows outside that reach are not shown."
	case "":
		return "Reach: (empty)—this version of alpacon does not recognize it, so the list may not be complete."
	default:
		return fmt.Sprintf("Reach: %s—this version of alpacon does not recognize it, so the list may not be complete.", utils.SanitizeServerHint(reach.Value))
	}
}
