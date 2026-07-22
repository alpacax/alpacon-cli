package server

import (
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// addDisruptiveActionFlags registers the shared -y/--force flags.
func addDisruptiveActionFlags(cmd *cobra.Command) {
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("force", false, "Override the busy guard even when the server has active user work")
}

// runDisruptiveServerAction runs the action with confirmation, MFA retry, and busy-guard handling.
func runDisruptiveServerAction(serverName, action, confirmMsg, successMsg, failMsg string, yes, force bool) {
	if !yes {
		utils.ConfirmAction(confirmMsg, serverName)
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	err = server.RequestServerAction(alpaconClient, serverName, action, force)
	if err != nil {
		err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
			OnMFARequired: func(srv string) error {
				return mfa.HandleMFAError(alpaconClient, srv)
			},
			CheckMFACompleted: func() (bool, error) {
				return mfa.CheckMFACompletion(alpaconClient)
			},
			RefreshToken: alpaconClient.RefreshToken,
			RetryOperation: func() error {
				return server.RequestServerAction(alpaconClient, serverName, action, force)
			},
		})
	}
	if err != nil {
		if code, _ := utils.ParseErrorResponse(err); code == utils.ServerBusyWithUserWork {
			if force {
				utils.CliErrorWithExit("Server '%s' is still busy with active user work "+
					"(open Websh/WebFTP session or in-flight command) despite --force. "+
					"Retry when idle", serverName)
			}
			utils.CliErrorWithExit("Server '%s' is busy with active user work "+
				"(open Websh/WebFTP session or in-flight command). "+
				"Retry when idle, or pass --force to override", serverName)
		}
		utils.CliErrorWithExit("%s: %s.", failMsg, err)
	}

	utils.CliSuccess("%s", successMsg)
}
