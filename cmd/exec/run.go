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

// sudoDenialLinePrefix is the exact terminal-facing denial line emitted by
// alpacon_approval.c via g_plugin_printf ("Alpacon denied this sudo command
// (CODE)."). The other "Permission denied (CODE)" form is assigned to *errstr,
// which only reaches the audit log—not the invoking terminal—so it must not be
// matched. Anchoring on this full prefix (not a bare "(CODE)") stops a command
// whose own output prints "(SUDO_RISK_DENIED)" from forging a hint.
const sudoDenialLinePrefix = "Alpacon denied this sudo command"

// sudoDenialHints maps a non-interactive sudo denial code to actionable
// guidance. Codes are kept in sync with alpacon-server utils/error_codes.py.
//
// The codes are UPPERCASE because alpacon_approval.c only passes [A-Z0-9_]
// codes through its sanitizer into the user-facing denial line (lowercase
// values are dropped). Each hint stays at the denial *category* level (what to
// do)—the server never sends the risk score or reasoning to a client.
var sudoDenialHints = []struct {
	code, guidance string
}{
	{
		"SUDO_NO_WORKSESSION_POLICY",
		"sudo was denied: this command is not covered by an MFA-bypass policy in your work session.\n" +
			"Add it and re-run (omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --sudo \"<command>\"\n",
	},
	{
		"SUDO_PRESENCE_REQUIRED",
		"sudo needs a recent MFA: complete a step-up, then re-run the command.\n",
	},
	{
		"SUDO_APPROVAL_REQUIRED",
		"sudo needs approval: an approval request was created. Re-run after a reviewer approves it.\n",
	},
	{
		"SUDO_RISK_DENIED",
		"sudo was denied by runtime risk assessment; this command is not permitted in this work session.\n",
	},
}

// denialCodePresent reports whether output contains the plugin's terminal denial
// line for the given code. It anchors on the full "Alpacon denied this sudo
// command (CODE)." line—including the trailing period the plugin emits—never a
// bare "(CODE)" token, so a command whose own output prints the token cannot
// forge a match on a command that succeeded. Both sudoDenialHint and
// hasSudoPresenceDenial route through here so the anchoring logic lives in one
// place.
func denialCodePresent(output, code string) bool {
	return strings.Contains(output, sudoDenialLinePrefix+" ("+code+").")
}

// sudoDenialHint returns actionable guidance when the command output shows a
// non-interactive sudo denial. Returns "" when no such denial is present.
func sudoDenialHint(output string) string {
	for _, h := range sudoDenialHints {
		if denialCodePresent(output, h.code) {
			return fmt.Sprintf("%s %s", utils.Yellow("Hint:"), h.guidance)
		}
	}
	return ""
}

// hasSudoPresenceDenial reports whether output carries the non-interactive sudo
// presence denial (SUDO_PRESENCE_REQUIRED), the only denial the CLI can resolve
// in-flow via an MFA step-up.
func hasSudoPresenceDenial(output string) bool {
	return denialCodePresent(output, "SUDO_PRESENCE_REQUIRED")
}

// RunExecWithPresenceStepUp runs a command via RunCommandWithRetry and, when it
// is denied for a missing recent MFA (SUDO_PRESENCE_REQUIRED) on an interactive
// terminal, offers an MFA step-up and retries once. Non-interactive callers
// (scripts, CI, AI agents) fall through unchanged so HandleCommandResult prints
// the static denial hint; non-interactive humans additionally get the
// verification link they can complete out of band. Only exec uses this; websh
// keeps its own sudo MFA flow.
func RunExecWithPresenceStepUp(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string) (string, error) {
	result, err := RunCommandWithRetry(ac, serverName, command, username, groupname, env, workSessionID)
	// A real presence denial makes sudo exit non-zero, so it always surfaces as a
	// RemoteCommandError carrying the denial line. Require that error as well as
	// the line match: a command that merely prints the line and SUCCEEDS
	// (err == nil) must not trigger a step-up and a re-run of a side-effecting
	// command.
	var remoteErr *event.RemoteCommandError
	if !errors.As(err, &remoteErr) || !hasSudoPresenceDenial(result) {
		return result, err
	}

	if !utils.IsInteractiveShell() {
		// Non-interactive: surface the verification link so a human reading the
		// logs can complete MFA out of band, then re-run. We cannot prompt or
		// open a browser here.
		if url, linkErr := mfa.GetMFALinkForSudo(ac, serverName); linkErr == nil && url != "" {
			utils.CliInfo("MFA verification link (open in a browser):\n  %s", url)
		}
		return result, err
	}

	if stepErr := mfa.StepUpForSudo(ac, serverName); stepErr != nil {
		utils.CliWarning("MFA step-up did not complete: %s", stepErr)
		return result, err
	}

	// Presence is fresh—retry once. Any remaining denial falls through to the
	// static hint in HandleCommandResult.
	return RunCommandWithRetry(ac, serverName, command, username, groupname, env, workSessionID)
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
