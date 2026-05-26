package worksession

import (
	"fmt"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	activeWorkSessionStatus    = "active"
	approvedWorkSessionStatus  = "approved"
	rejectedWorkSessionStatus  = "rejected"
	expiredWorkSessionStatus   = "expired"
	revokedWorkSessionStatus   = "revoked"
	completedWorkSessionStatus = "completed"
)

var unsetActiveWorkSession bool

var workSessionUseCmd = &cobra.Command{
	Use:   "use SESSION_ID",
	Short: "Set or clear the active work-session for the current workspace",
	Long: `Set the active work-session for the current workspace by passing its SESSION_ID.
Subsequent exec/websh/cp/tunnel commands attach to this session unless overridden with --work-session.
Pass --unset (with no SESSION_ID) to clear the active work-session.`,
	Example: `  alpacon work-session use ses-abc123
  alpacon work-session use --unset`,
	Run: func(cmd *cobra.Command, args []string) {
		if unsetActiveWorkSession {
			if len(args) > 0 {
				utils.CliErrorWithExit("--unset cannot be combined with a SESSION_ID argument")
			}
			// Treat missing config / no active workspace / empty entry as already-unset
			// so --unset is a true no-op and never surfaces a confusing config error.
			if cur, err := config.GetActiveWorkSession(); err != nil || cur == "" {
				utils.CliInfo("No active work-session to unset.")
				return
			}
			if err := RunUnset(); err != nil {
				utils.CliErrorWithExit("%s", err)
			}
			utils.CliSuccess("Active work-session cleared.")
			return
		}

		if len(args) != 1 {
			utils.CliErrorWithExit("SESSION_ID argument is required (or pass --unset)")
		}
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}
		desc, err := RunUse(ac, args[0])
		if err != nil {
			utils.CliErrorWithExit("%s", err)
		}
		if desc != "" {
			utils.CliSuccess("Active work-session set to %s (%s).", args[0], desc)
		} else {
			utils.CliSuccess("Active work-session set to %s.", args[0])
		}
	},
}

// RunUse validates the work-session via the server, then stores it in config.
// Returns the human-readable description on success.
func RunUse(ac *client.AlpaconClient, uuid string) (string, error) {
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return "", err
	}
	if ws.Status != activeWorkSessionStatus {
		return "", fmt.Errorf("work-session %s is in '%s' state and cannot be used", uuid, ws.Status)
	}
	if err := config.SetActiveWorkSession(uuid); err != nil {
		return "", err
	}
	return ws.Description, nil
}

// RunUnset clears the active work-session for the current workspace.
// Idempotent — no error when nothing is set.
func RunUnset() error {
	return config.SetActiveWorkSession("")
}

func init() {
	workSessionUseCmd.Flags().BoolVar(&unsetActiveWorkSession, "unset", false, "Clear the active work-session for the current workspace")
}
