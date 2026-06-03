package worksession

import (
	"strings"

	"github.com/alpacax/alpacon-cli/api/iam"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// resolveStatusFilter maps the --status flag to the API value. "all"
// (case-insensitive) clears the filter; any other value passes through.
func resolveStatusFilter(status string) string {
	status = strings.TrimSpace(status)
	if strings.EqualFold(status, "all") {
		return ""
	}
	return status
}

// resolveAssignedUser maps the --user flag to the assigned_user API value.
// "all" (case-insensitive) lists everyone; empty resolves to the current user
// via getCurrentUserID; any other value (a uuid) passes through.
func resolveAssignedUser(user string, getCurrentUserID func() (string, error)) (string, error) {
	user = strings.TrimSpace(user)
	switch {
	case strings.EqualFold(user, "all"):
		return "", nil
	case user == "":
		return getCurrentUserID()
	default:
		return user, nil
	}
}

// MarkActive decorates the row whose ID matches activeUUID with a "*" marker
// in its Active column. No-op when activeUUID is empty or no row matches.
// Exported for unit testing; safe to call with a nil/empty slice.
func MarkActive(rows []wsapi.WorkSessionAttributes, activeUUID string) {
	if activeUUID == "" {
		return
	}
	for i := range rows {
		if rows[i].ID == activeUUID {
			rows[i].Active = "*"
		}
	}
}

var workSessionListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List work sessions",
	Example: `  alpacon work-session ls                          # my active sessions (default)
  alpacon work-session ls --status all             # my sessions in any status
  alpacon work-session ls --user all               # everyone's active sessions
  alpacon work-session ls --user all --status all  # all sessions
  alpacon work-session ls --requester-type agent
  alpacon work-session ls --user <USER_ID> --status active`,
	Run: func(cmd *cobra.Command, args []string) {
		if requesterFilter != "" && requesterFilter != "user" && requesterFilter != "agent" {
			utils.CliErrorWithExit("Invalid --requester-type %q: must be \"user\" or \"agent\".", requesterFilter)
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		status := resolveStatusFilter(statusFilter)

		assignedUser, err := resolveAssignedUser(userFilter, func() (string, error) {
			currentUser, err := iam.GetCurrentUser(ac)
			if err != nil {
				return "", err
			}
			return currentUser.ID, nil
		})
		if err != nil {
			utils.CliErrorWithExit("Failed to resolve current user: %s.", err)
		}

		sessions, err := wsapi.GetWorkSessionList(ac, status, requesterFilter, assignedUser)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve work sessions: %s.", err)
		}

		// Best-effort active-session decoration; ignore config errors so listing still works.
		activeUUID, _ := config.GetActiveWorkSession()
		MarkActive(sessions, activeUUID)

		utils.PrintTable(sessions)
	},
}

func init() {
	workSessionListCmd.Flags().StringVar(&statusFilter, "status", "active", "Filter by status (pending, approved, active, completed, rejected, expired, revoked); use \"all\" for any status")
	workSessionListCmd.Flags().StringVar(&requesterFilter, "requester-type", "", "Filter by requester type (user, agent)")
	workSessionListCmd.Flags().StringVar(&userFilter, "user", "", "Filter by assigned user: default is self, \"all\" for everyone, or a user uuid")
}
