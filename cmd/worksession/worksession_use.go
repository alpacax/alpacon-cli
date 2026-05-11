package worksession

import (
	"errors"
	"fmt"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const activeWorkSessionStatus = "active"

var unsetActiveWorkSession bool

// RunUse validates the work-session via the server, then stores it in config.
// Returns the human-readable description on success.
func RunUse(ac *client.AlpaconClient, uuid string) (string, error) {
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return "", err
	}
	if ws == nil {
		return "", fmt.Errorf("work session not found: %s", uuid)
	}
	if ws.Status != activeWorkSessionStatus {
		return "", fmt.Errorf("work session %s is in '%s' state and cannot be used", uuid, ws.Status)
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

var workSessionUseCmd = &cobra.Command{
	Use:   "use [UUID]",
	Short: "Set or clear the active work-session for the current workspace",
	Long: `Set the active work-session UUID for the current workspace.
Subsequent exec/websh/cp/tunnel commands attach to this session unless overridden with --work-session.
Use --unset to clear.`,
	Example: `  alpacon work-session use ses-abc123
  alpacon work-session use --unset`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if unsetActiveWorkSession {
			if len(args) > 0 {
				return errors.New("--unset cannot be combined with a UUID argument")
			}
			cur, _ := config.GetActiveWorkSession()
			if err := RunUnset(); err != nil {
				return err
			}
			if cur == "" {
				utils.CliInfo("No active work-session to unset.")
			} else {
				utils.CliSuccess("Active work-session cleared.")
			}
			return nil
		}

		if len(args) != 1 {
			return errors.New("UUID argument is required (or pass --unset)")
		}
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			return fmt.Errorf("connection to Alpacon API failed: %w (consider re-logging)", err)
		}
		desc, err := RunUse(ac, args[0])
		if err != nil {
			return err
		}
		if desc != "" {
			utils.CliSuccess("Active work-session set to %s (%s).", args[0], desc)
		} else {
			utils.CliSuccess("Active work-session set to %s.", args[0])
		}
		return nil
	},
}

func init() {
	workSessionUseCmd.Flags().BoolVar(&unsetActiveWorkSession, "unset", false, "Clear the active work-session for the current workspace")
}
