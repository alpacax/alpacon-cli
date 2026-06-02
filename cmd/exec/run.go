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
//
// The form is UPPERCASE because alpacon_approval.c only passes [A-Z0-9_]
// codes through its sanitizer into the user-facing denial message; lowercase
// values are dropped, so this is the form that actually reaches stderr as
// "Permission denied (SUDO_NO_WORKSESSION_POLICY)".
const sudoNoWorksessionPolicyCode = "SUDO_NO_WORKSESSION_POLICY"

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

// RunCommandWithRetry executes a remote command with MFA/username-required error
// handling and retry logic, streaming output to stdout. Used by exec and websh.
// workSessionID is forwarded as the work_session field; pass "" to omit it.
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

// HandleCommandResult exits appropriately on error. Output is streamed to stdout
// during execution; on a remote failure the error carries that output, used here
// only to surface the sudo-denial hint (not re-printed).
func HandleCommandResult(err error) {
	if err != nil {
		var remoteErr *event.RemoteCommandError
		if errors.As(err, &remoteErr) {
			stderrLine, exitCode := remoteCommandOutcome(remoteErr)
			if stderrLine != "" {
				fmt.Fprint(os.Stderr, stderrLine)
			}
			if hint := sudoDenialHint(remoteErr.Output); hint != "" {
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

// remoteCommandOutcome renders the stderr phase line and exit code for a remote
// command failure. The command's stdout was already streamed during execution,
// so it is not re-emitted here. stderrLine already includes its trailing newline.
func remoteCommandOutcome(remoteErr *event.RemoteCommandError) (stderrLine string, exitCode int) {
	if remoteErr.ErrorPhase != "" {
		stderrLine = fmt.Sprintf("%s: [%s] %s\n",
			utils.Red("Error"),
			remoteErr.ErrorPhase,
			event.DescribePhase(remoteErr.ErrorPhase))
	}
	return stderrLine, remoteErr.ExitCode
}
