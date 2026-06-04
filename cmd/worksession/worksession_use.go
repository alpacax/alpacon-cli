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
				utils.CliUsageErrorEnvelopeWithExit(opUnset, "--unset cannot be combined with a SESSION_ID argument")
			}
			// Treat missing config / no active workspace / empty entry as already-unset
			// so --unset is a true no-op and never surfaces a confusing config error.
			if cur, err := config.GetActiveWorkSession(); err != nil || cur == "" {
				if utils.OutputFormat == utils.OutputFormatJSON {
					printWorkSessionMutationJSON(workSessionMutationOutput{
						OK:                true,
						Operation:         opUnset,
						Message:           "No active work-session to unset.",
						ActiveWorksession: nil,
					})
					return
				}
				utils.CliInfo("No active work-session to unset.")
				return
			}
			if err := RunUnset(); err != nil {
				utils.CliErrorEnvelopeWithExit(opUnset, err, "%s", err)
			}
			if utils.OutputFormat == utils.OutputFormatJSON {
				printWorkSessionMutationJSON(workSessionMutationOutput{
					OK:                true,
					Operation:         opUnset,
					Message:           "Active work-session cleared.",
					ActiveWorksession: nil,
				})
				return
			}
			utils.CliSuccess("Active work-session cleared.")
			return
		}

		if len(args) != 1 {
			utils.CliUsageErrorEnvelopeWithExit(opUse, "SESSION_ID argument is required (or pass --unset)")
		}
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opUse, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}
		ws, err := RunUseSession(ac, args[0])
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opUse, err, "%s", err)
		}
		message := activeWorkSessionSetMessage("", ws.ID, ws.Description)
		if utils.OutputFormat == utils.OutputFormatJSON {
			active := ws.ID
			printWorkSessionMutationJSON(newWorkSessionMutationOutput(opUse, message, ws, &active))
			return
		}
		utils.CliSuccess("%s", message)
	},
}

// RunUse validates the work-session via the server, then stores it in config.
// Returns the human-readable description on success.
func RunUse(ac *client.AlpaconClient, uuid string) (string, error) {
	ws, err := RunUseSession(ac, uuid)
	if err != nil {
		return "", err
	}
	return ws.Description, nil
}

// RunUseSession validates the work-session via the server, then stores it in config.
func RunUseSession(ac *client.AlpaconClient, uuid string) (*wsapi.WorkSession, error) {
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return nil, err
	}
	if ws.Status != activeWorkSessionStatus {
		return nil, fmt.Errorf("work-session %s is in '%s' state and cannot be used", ws.ID, ws.Status)
	}
	// Persist the canonical ID from the API rather than the raw argument so config
	// stays consistent with server-side canonicalization and the printed JSON fields.
	if err := config.SetActiveWorkSession(ws.ID); err != nil {
		return nil, err
	}
	return ws, nil
}

// RunUnset clears the active work-session for the current workspace.
// Idempotent — no error when nothing is set.
func RunUnset() error {
	return config.SetActiveWorkSession("")
}

func init() {
	workSessionUseCmd.Flags().BoolVar(&unsetActiveWorkSession, "unset", false, "Clear the active work-session for the current workspace")
}
