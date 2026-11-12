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
func RunDisruptiveServerAction(serverName, action, confirmMsg, successMsg, failMsg string, yes, force bool) error {
	if !yes {
		if proceed, err := utils.Confirm(confirmMsg, serverName); err != nil || !proceed {
			return err
		}
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	if err := requestServerActionWithMFA(alpaconClient, serverName, action, force); err != nil {
		if code, _ := utils.ParseErrorResponse(err); code == utils.ServerBusyWithUserWork {
			return utils.NewExitErrorWithCode(utils.ExitCodeServerBusy, "%s", busyGuardMessage(serverName, force))
		}
		return utils.NewExitError("%s: %s.", failMsg, err)
	}

	utils.CliSuccess("%s", successMsg)
	return nil
}

// RunServerAction runs a non-disruptive action with confirmation and MFA retry.
// The server applies no busy guard to it, so there is no --force to pass.
func RunServerAction(serverName, action, confirmMsg, successMsg, failMsg string, yes bool) error {
	if !yes {
		if proceed, err := utils.Confirm(confirmMsg, serverName); err != nil || !proceed {
			return err
		}
	}

	alpaconClient, err := client.NewAlpaconAPIClient()
	if err != nil {
		return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	if err := requestServerActionWithMFA(alpaconClient, serverName, action, false); err != nil {
		return utils.NewExitError("%s: %s.", failMsg, err)
	}

	utils.CliSuccess("%s", successMsg)
	return nil
}

// requestServerActionWithMFA submits the action and, when the workspace asks
// for MFA, walks the user through it and retries until the step-up clears.
func requestServerActionWithMFA(alpaconClient *client.AlpaconClient, serverName, action string, force bool) error {
	err := server.RequestServerAction(alpaconClient, serverName, action, force)
	if err != nil {
		err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
			OnMFARequired: func(srv string) error {
				return mfa.HandleMFAError(alpaconClient, srv)
			},
			RetryOperation: func() error {
				return server.RequestServerAction(alpaconClient, serverName, action, force)
			},
		})
	}
	return err
}

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
