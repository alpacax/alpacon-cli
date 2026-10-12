package server

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// AddConfirmFlag registers the shared -y flag of an action that asks before it runs.
func AddConfirmFlag(cmd *cobra.Command) {
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

// AddDisruptiveActionFlags registers the shared -y/--force flags.
func AddDisruptiveActionFlags(cmd *cobra.Command) {
	AddConfirmFlag(cmd)
	cmd.Flags().Bool("force", false, "Override the busy guard even when the server has active user work")
}

// RunDisruptiveServerAction runs the action with confirmation, MFA retry, and busy-guard handling.
func RunDisruptiveServerAction(serverName, action, confirmMsg, successMsg, failMsg string, yes, force bool) {
	if !yes {
		utils.ConfirmAction(confirmMsg, serverName)
	}

	if err := requestServerActionWithMFA(serverName, action, force); err != nil {
		if code, _ := utils.ParseErrorResponse(err); code == utils.ServerBusyWithUserWork {
			utils.CliErrorWithExitCode(utils.ExitCodeServerBusy, "%s", busyGuardMessage(serverName, force))
		}
		utils.CliErrorWithExit("%s: %s.", failMsg, err)
	}

	utils.CliSuccess("%s", successMsg)
}

// RunServerAction runs a non-disruptive action with confirmation and MFA retry.
// The server applies no busy guard to it, so there is no --force to pass.
func RunServerAction(serverName, action, confirmMsg, successMsg, failMsg string, yes bool) {
	if !yes {
		utils.ConfirmAction(confirmMsg, serverName)
	}

	if err := requestServerActionWithMFA(serverName, action, false); err != nil {
		utils.CliErrorWithExit("%s: %s.", failMsg, err)
	}

	utils.CliSuccess("%s", successMsg)
}

// requestServerActionWithMFA submits the action and, when the workspace asks
// for MFA, walks the user through it and retries once.
func requestServerActionWithMFA(serverName, action string, force bool) error {
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
	return err
}

// busyGuardMessage returns the busy-guard refusal text, tailored to whether --force was already used.
func busyGuardMessage(serverName string, force bool) string {
	if force {
		return fmt.Sprintf("Server '%s' is still busy with active user work "+
			"(open Websh/WebFTP session or in-flight command) despite --force. "+
			"Retry when idle", serverName)
	}
	return fmt.Sprintf("Server '%s' is busy with active user work "+
		"(open Websh/WebFTP session or in-flight command). "+
		"Retry when idle, or pass --force to override", serverName)
}
