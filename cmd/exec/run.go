package exec

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// RunCommandWithRetry executes a remote command with MFA and username-required
// error handling and retry logic. Used by both exec and websh commands.
func RunCommandWithRetry(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string) (string, error) {
	result, err := event.RunCommand(ac, serverName, command, username, groupname, env)
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
				result, err = event.RunCommand(ac, serverName, command, username, groupname, env)
				return err
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to execute command on '%s' server: %w", serverName, err)
		}
	}
	return result, nil
}
