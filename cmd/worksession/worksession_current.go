package worksession

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

// projectWorkSessionAttributes mirrors GetWorkSessionList's projection so the
// `current` table output matches the columns and formatting of `ls`.
func projectWorkSessionAttributes(ws *wsapi.WorkSession) wsapi.WorkSessionAttributes {
	serverNames := make([]string, len(ws.Servers))
	for i, srv := range ws.Servers {
		serverNames[i] = srv.Name
	}
	return wsapi.WorkSessionAttributes{
		ID:          ws.ID,
		Description: utils.TruncateString(ws.Description, 70),
		Status:      ws.Status,
		Scopes:      strings.Join(ws.Scopes, ", "),
		Servers:     strings.Join(serverNames, ", "),
		ExpiresAt:   ws.ExpiresAt.Local().Format("2006-01-02 15:04"),
	}
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
		uuid, ws, err := RunCurrent(ac)
		if err != nil {
			if uuid != "" {
				return fmt.Errorf("active work-session %s no longer accessible: %w. Run 'alpacon work-session use --unset' to clear", uuid, err)
			}
			return err
		}
		if uuid == "" {
			if utils.OutputFormat == utils.OutputFormatJSON {
				_, _ = fmt.Fprintln(os.Stdout, "null")
			} else {
				utils.CliInfo("No active work-session.")
			}
			return nil
		}
		if utils.OutputFormat == utils.OutputFormatJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(ws)
		}
		utils.PrintTable([]wsapi.WorkSessionAttributes{projectWorkSessionAttributes(ws)})
		return nil
	},
}
