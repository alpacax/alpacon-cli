package worksession

import (
	"encoding/json"
	"fmt"
	"os"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var workSessionCurrentCmd = &cobra.Command{
	Use:     "current",
	Short:   "Show the active work-session in this workspace",
	Example: `  alpacon work-session current`,
	Run: func(cmd *cobra.Command, args []string) {
		if utils.OutputFormat == utils.OutputFormatJSON {
			if err := printCurrentRaw(); err != nil {
				utils.CliErrorEnvelopeWithExit(opCurrent, err, "%s", err)
			}
			return
		}
		if err := printCurrentTable(); err != nil {
			utils.CliErrorEnvelopeWithExit(opCurrent, err, "%s", err)
		}
	},
}

// RunCurrent returns the active work-session UUID and its server-side details.
// Returns ("", nil, nil) when nothing is set, or (uuid, nil, err) if set but unreachable.
func RunCurrent(ac *client.AlpaconClient) (string, *wsapi.WorkSession, error) {
	uuid, err := config.GetActiveWorkSession()
	if err != nil {
		return "", nil, err
	}
	if uuid == "" {
		return "", nil, nil
	}
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return uuid, nil, err
	}
	return uuid, ws, nil
}

// printCurrentRaw outputs the active session as raw JSON without field projection.
func printCurrentRaw() error {
	uuid, err := config.GetActiveWorkSession()
	if err != nil {
		return err
	}
	if uuid == "" {
		_, _ = fmt.Fprintln(os.Stdout, "null")
		return nil
	}
	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		return fmt.Errorf("connection to Alpacon API failed: %w (consider re-logging)", err)
	}
	return WriteCurrentRaw(ac, uuid)
}

func WriteCurrentRaw(ac *client.AlpaconClient, uuid string) error {
	body, err := wsapi.GetWorkSessionRaw(ac, uuid)
	if err != nil {
		return staleActiveSessionError(uuid, err)
	}
	var session struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &session) == nil {
		warnIfEnded(uuid, session.Status)
	}
	utils.PrintJson(body)
	return nil
}

// printCurrentTable projects the active session through ProjectAttributes so
// the columns stay consistent with 'work-session ls', and marks it as 'ls' does.
func printCurrentTable() error {
	uuid, err := config.GetActiveWorkSession()
	if err != nil {
		return err
	}
	if uuid == "" {
		utils.CliInfo("No active work-session.")
		return nil
	}
	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		return fmt.Errorf("connection to Alpacon API failed: %w (consider re-logging)", err)
	}
	return WriteCurrentTable(ac, uuid)
}

func WriteCurrentTable(ac *client.AlpaconClient, uuid string) error {
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return staleActiveSessionError(uuid, err)
	}
	rows := []wsapi.WorkSessionAttributes{wsapi.ProjectAttributes(ws)}
	MarkActive(rows, uuid)
	warnIfEnded(uuid, ws.Status)
	utils.PrintTable(rows)
	return nil
}

func warnIfEnded(uuid, status string) {
	if wsapi.IsEndedStatus(status) {
		utils.CliWarning("Active work-session %s has ended (%s). Run 'alpacon work-session use --unset' to clear it.", uuid, status)
	}
}

func staleActiveSessionError(uuid string, cause error) error {
	return fmt.Errorf("active work-session %s no longer accessible: %w (run 'alpacon work-session use --unset' to clear)", uuid, cause)
}
