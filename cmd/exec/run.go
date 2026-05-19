package exec

import (
	"errors"
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// RunCommandWithRetry executes a remote command with MFA and username-required
// error handling and retry logic. Used by both exec and websh commands.
// workSessionID is forwarded to the server as the work_session field; pass ""
// to omit it.
func RunCommandWithRetry(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string) (string, error) {
	result, err := event.RunCommand(ac, serverName, command, username, groupname, env, workSessionID)
	var remoteErr *event.RemoteCommandError
	if errors.As(err, &remoteErr) {
		return result, remoteErr
	}
	if err != nil {
		err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
			OnMFARequired: func(srv string) error {
				return mfa.HandleMFAError(ac, srv)
			},
			OnUsernameRequired: func() error {
				_, err := iam.HandleUsernameRequired()
				return err
			},
			CheckMFACompleted: func() (bool, error) {
				return mfa.CheckMFACompletion(ac)
			},
			RefreshToken: ac.RefreshToken,
			RetryOperation: func() error {
				result, err = event.RunCommand(ac, serverName, command, username, groupname, env, workSessionID)
				return err
			},
		})
		// RetryOperation may return a RemoteCommandError; re-check after HandleCommonErrors.
		if errors.As(err, &remoteErr) {
			return result, remoteErr
		}
		if err != nil {
			return "", fmt.Errorf("failed to execute command on '%s' server: %w", serverName, err)
		}
	}
	return result, nil
}

// HandleCommandResult prints result on success, or exits appropriately on error.
func HandleCommandResult(result string, err error) {
	if err != nil {
		var remoteErr *event.RemoteCommandError
		if errors.As(err, &remoteErr) {
			if result != "" {
				fmt.Println(result)
			}
			if remoteErr.ErrorPhase != "" {
				fmt.Fprintf(os.Stderr, "%s: remote command failed: %s\n", utils.Red("Error"), remoteErr.ErrorPhase)
			}
			os.Exit(remoteErr.ExitCode)
		}
		utils.CliErrorWithExit("%s", err)
		return
	}
	fmt.Println(result)
}
