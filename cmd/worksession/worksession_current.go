package worksession

import (
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
	Short:   "Show the active work-session for the current workspace",
	Example: `  alpacon work-session current`,
	Run: func(cmd *cobra.Command, args []string) {
		if utils.OutputFormat == utils.OutputFormatJSON {
			if err := printCurrentRaw(); err != nil {
				utils.CliErrorWithExit("%s", err)
			}
			return
		}
		if err := printCurrentTable(); err != nil {
			utils.CliErrorWithExit("%s", err)
		}
	},
}

// RunCurrent returns the active work-session UUID and the fetched session detail
// for the current workspace. Returns ("", nil, nil) when nothing is set.
// Returns (uuid, nil, err) when the UUID is set but the server cannot resolve it.
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

// printCurrentRaw renders the server's JSON response through utils.PrintJson.
// All fields and the server's key order are preserved; whitespace and
// indentation are normalized to match the project-wide --output json
// convention (pretty-printed, 2-space indent).
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
	body, err := wsapi.GetWorkSessionRaw(ac, uuid)
	if err != nil {
		return staleActiveSessionError(uuid, err)
	}
	utils.PrintJson(body)
	return nil
}

// printCurrentTable projects the active session through ProjectAttributes so
// the columns stay consistent with 'work-session ls', and marks the active row.
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
	ws, err := wsapi.GetWorkSession(ac, uuid)
	if err != nil {
		return staleActiveSessionError(uuid, err)
	}
	row := wsapi.ProjectAttributes(ws)
	row.Active = "*"
	utils.PrintTable([]wsapi.WorkSessionAttributes{row})
	return nil
}

func staleActiveSessionError(uuid string, cause error) error {
	return fmt.Errorf("active work-session %s no longer accessible: %w (run 'alpacon work-session use --unset' to clear)", uuid, cause)
}
