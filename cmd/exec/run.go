package exec

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// sudoNoWorksessionPolicyCode is the server error code surfaced in command
// output when a non-interactive sudo is denied for lack of a matching
// MFA-bypass policy in the work session. Kept in sync with
// alpacon-server utils/error_codes.py ErrorCode.SUDO_NO_WORKSESSION_POLICY.
const sudoNoWorksessionPolicyCode = "sudo_no_worksession_policy"

// sudoDenialHint returns actionable guidance when the command output shows a
// non-interactive sudo denial, telling the caller how to authorize the command
// via their work session. Returns "" when no such denial is present.
func sudoDenialHint(output string) string {
	if !strings.Contains(output, sudoNoWorksessionPolicyCode) {
		return ""
	}
	return fmt.Sprintf(
		"%s sudo was denied: this command is not covered by an MFA-bypass policy in your work session.\n"+
			"Add it and re-run (omit SESSION_ID to use the active session):\n"+
			"  alpacon work-session update [SESSION_ID] --sudo \"<command>\"\n",
		utils.Yellow("Hint:"),
	)
}

// RunCommandWithRetry executes a remote command with MFA and username-required
// error handling and retry logic. Used by both exec and websh commands.
// workSessionID is forwarded to the server as the work_session field; pass ""
// to omit it.
func RunCommandWithRetry(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string) (string, error) {
	result, err := event.RunCommand(ac, serverName, command, username, groupname, env, workSessionID)
	if phased, ok := asPhasedError(err); ok {
		return result, phased
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
		// RetryOperation may surface a phased error; re-check after HandleCommonErrors.
		if phased, ok := asPhasedError(err); ok {
			return result, phased
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
			stdoutLine, stderrLine, exitCode := remoteCommandOutcome(result, remoteErr)
			if stdoutLine != "" {
				fmt.Println(stdoutLine)
			}
			if stderrLine != "" {
				fmt.Fprint(os.Stderr, stderrLine)
			}
			if hint := sudoDenialHint(result); hint != "" {
				fmt.Fprint(os.Stderr, hint)
			}
			os.Exit(exitCode)
		}
		var clientTimeout *event.ClientTimeoutError
		if errors.As(err, &clientTimeout) {
			fmt.Fprint(os.Stderr, clientTimeoutLine())
			os.Exit(1)
		}
		utils.CliErrorWithExit("%s", err)
		return
	}
	fmt.Println(result)
	if hint := sudoDenialHint(result); hint != "" {
		fmt.Fprint(os.Stderr, hint)
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
func remoteCommandOutcome(result string, remoteErr *event.RemoteCommandError) (stdoutLine, stderrLine string, exitCode int) {
	if result != "" {
		stdoutLine = result
	}
	if remoteErr.ErrorPhase != "" {
		stderrLine = fmt.Sprintf("%s: [%s] %s\n",
			utils.Red("Error"),
			remoteErr.ErrorPhase,
			event.DescribePhase(remoteErr.ErrorPhase))
	}
	return stdoutLine, stderrLine, remoteErr.ExitCode
}
