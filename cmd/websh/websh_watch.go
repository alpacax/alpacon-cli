package websh

import (
	"encoding/json"
	"fmt"
	"io"
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

		printWatchHeader(os.Stderr, detail)

		session, err := websh.ConnectToSession(alpaconClient, sessionID)
		if err != nil {
			utils.CliErrorWithExit("Failed to watch websh session: %s.", err)
		}

		if err = websh.OpenReadOnlyTerminal(alpaconClient, session); err != nil {
			utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, "Websh watch session ended with error: %s.", err)
		}
	},
}

// This header is the only sign of whose session is on screen, and the names in
// it are set by whoever registered the server or the account.
func printWatchHeader(w io.Writer, detail websh.SessionDetailResponse) {
	fmt.Fprintf(w, "\nSession:  %s\n", utils.SanitizeTerminalText(detail.ID))
	fmt.Fprintf(w, "Server:   %s\n", utils.SanitizeTerminalText(detail.Server.Name))
	fmt.Fprintf(w, "User:     %s\n", utils.SanitizeTerminalText(detail.User.Name))
	fmt.Fprintf(w, "Username: %s\n", utils.SanitizeTerminalText(detail.Username))
	fmt.Fprintf(w, "\nWatching in read-only mode. Press Ctrl+C to exit.\n\n")
}
