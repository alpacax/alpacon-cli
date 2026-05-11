package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

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
	Example: `  alpacon work-session ls
  alpacon work-session ls --status active
  alpacon work-session ls --requester-type agent`,
	Run: func(cmd *cobra.Command, args []string) {
		if requesterFilter != "" && requesterFilter != "user" && requesterFilter != "agent" {
			utils.CliErrorWithExit("Invalid --requester-type %q: must be \"user\" or \"agent\".", requesterFilter)
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		sessions, err := wsapi.GetWorkSessionList(ac, statusFilter, requesterFilter)
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
	workSessionListCmd.Flags().StringVar(&statusFilter, "status", "", "Filter by status (pending, approved, active, completed, rejected, expired, revoked)")
	workSessionListCmd.Flags().StringVar(&requesterFilter, "requester-type", "", "Filter by requester type (user, agent)")
}
