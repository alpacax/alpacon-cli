package websh

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webshWatchCmd = &cobra.Command{
	Use:     "watch SESSION_ID",
	Short:   "Watch an active websh session (staff/superuser only)",
	Example: `  alpacon websh watch abc123`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sessionID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		detailBytes, err := websh.GetSessionDetail(alpaconClient, sessionID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve session info: %s.", err)
		}
		var detail websh.SessionDetailResponse
		if err = json.Unmarshal(detailBytes, &detail); err != nil {
			utils.CliErrorWithExit("Failed to parse session info: %s.", err)
		}

		fmt.Fprintf(os.Stderr, "\nSession:  %s\n", detail.ID)
		fmt.Fprintf(os.Stderr, "Server:   %s\n", detail.Server.Name)
		fmt.Fprintf(os.Stderr, "User:     %s\n", detail.User.Name)
		fmt.Fprintf(os.Stderr, "Username: %s\n", detail.Username)
		fmt.Fprintf(os.Stderr, "\nWatching in read-only mode. Press Ctrl+C to exit.\n\n")

		session, err := websh.ConnectToSession(alpaconClient, sessionID)
		if err != nil {
			utils.CliErrorWithExit("Failed to watch websh session: %s.", err)
		}

		if err = websh.OpenReadOnlyTerminal(alpaconClient, session); err != nil {
			utils.CliErrorWithExit("websh watch session ended with error: %s.", err)
		}
	},
}
