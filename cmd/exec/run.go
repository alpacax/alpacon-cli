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
//
// Output is streamed to os.Stdout during execution (not buffered).
func RunCommandWithRetry(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string) error {
	err := event.RunCommandStreaming(ac, serverName, command, username, groupname, env, workSessionID)
	if phased, ok := asPhasedError(err); ok {
		return phased
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
				return event.RunCommandStreaming(ac, serverName, command, username, groupname, env, workSessionID)
			},
		})
		// RetryOperation may surface a phased error; re-check after HandleCommonErrors.
		if phased, ok := asPhasedError(err); ok {
			return phased
		}
		if err != nil {
			return fmt.Errorf("failed to execute command on '%s' server: %w", serverName, err)
		}
	}
	return nil
}

// HandleCommandResult exits appropriately on error. Chunks are already streamed
// to stdout during execution, so no result string is needed.
func HandleCommandResult(err error) {
	if err != nil {
		var remoteErr *event.RemoteCommandError
		if errors.As(err, &remoteErr) {
			stdoutLine, stderrLine, exitCode := remoteCommandOutcome(remoteErr)
			if stdoutLine != "" {
				fmt.Println(stdoutLine)
			}
			if stderrLine != "" {
				fmt.Fprint(os.Stderr, stderrLine)
			}
			os.Exit(exitCode)
		}
		var clientTimeout *event.ClientTimeoutError
		if errors.As(err, &clientTimeout) {
			fmt.Fprint(os.Stderr, clientTimeoutLine())
			os.Exit(1)
		}
		utils.CliErrorWithExit("%s", err)
	}
}

// asPhasedError unwraps err to a RemoteCommandError or ClientTimeoutError.
func asPhasedError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	var remoteErr *event.RemoteCommandError
	if errors.As(err, &remoteErr) {
		return remoteErr, true
	}
	var clientTimeout *event.ClientTimeoutError
	if errors.As(err, &clientTimeout) {
		return clientTimeout, true
	}
	return nil, false
}

// clientTimeoutLine renders the stderr line for ClientTimeoutError (with newline).
func clientTimeoutLine() string {
	const phase = "client_timeout"
	return fmt.Sprintf("%s: [%s] %s\n", utils.Red("Error"), phase, event.DescribePhase(phase))
}

func detachResultLines(jobID string) (string, string) {
	return fmt.Sprintf("Job submitted: %s", jobID),
		fmt.Sprintf("Run `alpacon exec logs %s` to check the result.", jobID)
}

// remoteCommandOutcome is the testable core of HandleCommandResult's
// RemoteCommandError branch. stderrLine already includes its trailing newline.
func remoteCommandOutcome(remoteErr *event.RemoteCommandError) (stdoutLine, stderrLine string, exitCode int) {
	if remoteErr.Output != "" {
		stdoutLine = remoteErr.Output
	}
	if remoteErr.ErrorPhase != "" {
		stderrLine = fmt.Sprintf("%s: [%s] %s\n",
			utils.Red("Error"),
			remoteErr.ErrorPhase,
			event.DescribePhase(remoteErr.ErrorPhase))
	}
	return stdoutLine, stderrLine, remoteErr.ExitCode
}
