package server

import (
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// runDisruptiveServerAction runs a disruptive system action (reboot/shutdown/
// upgrade) with local confirmation, MFA retry, and busy-guard handling.
// confirmMsg takes the server name; failMsg is prefixed to ": <err>.".
func runDisruptiveServerAction(serverName, action, confirmMsg, successMsg, failMsg string, yes, force bool) {
	if !yes {
		utils.ConfirmAction(confirmMsg, serverName)
	}

	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
	}

	err = server.RequestServerAction(ac, serverName, action, force)
	if err != nil {
		err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
			OnMFARequired: func(srv string) error {
				return mfa.HandleMFAError(ac, srv)
			},
			CheckMFACompleted: func() (bool, error) {
				return mfa.CheckMFACompletion(ac)
			},
			RefreshToken: ac.RefreshToken,
			RetryOperation: func() error {
				return server.RequestServerAction(ac, serverName, action, force)
			},
		})
	}
	if err != nil {
		if code, _ := utils.ParseErrorResponse(err); code == utils.ServerBusyWithUserWork {
			utils.CliErrorWithExit("Server '%s' is busy with active user work "+
				"(open websh/webftp session or in-flight command). "+
				"Retry when idle, or pass --force to override", serverName)
		}
		utils.CliErrorWithExit(failMsg+": %s.", err)
	}

	utils.CliSuccess("%s", successMsg)
}
