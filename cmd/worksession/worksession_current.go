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

var workSessionCurrentCmd = &cobra.Command{
	Use:     "current",
	Short:   "Show the active work-session for the current workspace",
	Example: `  alpacon work-session current`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			return fmt.Errorf("connection to Alpacon API failed: %w (consider re-logging)", err)
		}

		// JSON mode: fetch raw server response and print as-is to preserve all fields and key order.
		if utils.OutputFormat == utils.OutputFormatJSON {
			uuid, err := config.GetActiveWorkSession()
			if err != nil {
				return err
			}
			if uuid == "" {
				_, _ = fmt.Fprintln(os.Stdout, "null")
				return nil
			}
			body, err := wsapi.GetWorkSessionRaw(ac, uuid)
			if err != nil {
				return fmt.Errorf("active work-session %s no longer accessible: %w. Run 'alpacon work-session use --unset' to clear", uuid, err)
			}
			utils.PrintJson(body)
			return nil
		}

		// Table mode: use RunCurrent (parsed) so we can project the row consistently with `ls`.
		uuid, ws, err := RunCurrent(ac)
		if err != nil {
			if uuid != "" {
				return fmt.Errorf("active work-session %s no longer accessible: %w. Run 'alpacon work-session use --unset' to clear", uuid, err)
			}
			return err
		}
		if uuid == "" {
			utils.CliInfo("No active work-session.")
			return nil
		}
		utils.PrintTable([]wsapi.WorkSessionAttributes{wsapi.ProjectAttributes(ws)})
		return nil
	},
}
