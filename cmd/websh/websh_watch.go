package websh

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newWatchCommand(newClient clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:     "watch SESSION_ID",
		Short:   "Watch a Websh session (staff/superuser only)",
		Example: `  alpacon websh watch 550e8400-e29b-41d4-a716-446655440000`,
		Args:    cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			detailBytes, err := websh.GetSessionDetail(alpaconClient, sessionID)
			if err != nil {
				return utils.NewExitError("Failed to retrieve session info: %s.", err)
			}
			var detail websh.SessionDetailResponse
			if err = json.Unmarshal(detailBytes, &detail); err != nil {
				return utils.NewExitError("Failed to parse session info: %s.", err)
			}

			printWatchHeader(cmd.ErrOrStderr(), detail)

			session, err := websh.ConnectToSession(alpaconClient, sessionID)
			if err != nil {
				return utils.NewExitError("Failed to watch websh session: %s.", utils.InteractiveOnly(err, "watch a Websh session"))
			}

			if err = websh.OpenReadOnlyTerminal(alpaconClient, session); err != nil {
				return utils.NewExitErrorWithCode(utils.ExitCodeGeneralError, "Websh watch session ended with error: %s.", err)
			}
			return nil
		}),
	}
}

// printWatchHeader sanitizes every name: the header is the only sign of whose session is on
// screen, and the names in it are set by whoever registered the server or the account.
func printWatchHeader(w io.Writer, detail websh.SessionDetailResponse) {
	fmt.Fprintf(w, "\nSession:  %s\n", utils.SanitizeTerminalText(detail.ID))
	fmt.Fprintf(w, "Server:   %s\n", utils.SanitizeTerminalText(detail.Server.Name))
	fmt.Fprintf(w, "User:     %s\n", utils.SanitizeTerminalText(detail.User.Name))
	fmt.Fprintf(w, "Username: %s\n", utils.SanitizeTerminalText(detail.Username))
	fmt.Fprintf(w, "\nWatching in read-only mode. Press Ctrl+C to exit.\n\n")
}
