package exec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	// approvalWaitPollInterval and approvalWaitTimeout bound the --wait poll for a
	// SUDO_APPROVAL_REQUIRED denial. The interval is slower than the MFA step-up
	// poll (api/mfa/mfa.go) because each tick re-submits and re-runs the remote
	// command, and a human approving out of band in the console works on a
	// seconds-to-minutes timescale, not sub-second. The 3-minute ceiling mirrors
	// maxRetryDuration in utils/error_handler.go so the two waits feel consistent.
	approvalWaitPollInterval = 5 * time.Second
	approvalWaitTimeout      = 3 * time.Minute
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

// hasSudoApprovalDenial reports whether output carries the sudo approval-required
// denial (SUDO_APPROVAL_REQUIRED): a human must approve the request out of band
// in the Alpacon console before the command can run. Like the other detectors it
// anchors on the plugin's exact terminal denial line via denialCodePresent, so a
// command that merely prints the token in its own output cannot forge a pending
// signal or wedge --wait into an indefinite re-run loop.
func hasSudoApprovalDenial(output string) bool {
	return denialCodePresent(output, "SUDO_APPROVAL_REQUIRED")
}

// RunExecWithPresenceStepUp runs a command via RunCommandWithRetry and, when it
// is denied for a missing recent MFA (SUDO_PRESENCE_REQUIRED) on an interactive
// terminal, offers an MFA step-up and retries once. Non-interactive callers
// (scripts, CI, AI agents) fall through unchanged so HandleCommandResult prints
// the static denial hint; non-interactive humans additionally get the
// verification link they can complete out of band. Only exec uses this; websh
// keeps its own sudo MFA flow.
func RunExecWithPresenceStepUp(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, out io.Writer) error {
	err := RunCommandWithRetry(ac, serverName, command, username, groupname, env, workSessionID, out)
	// A real presence denial makes sudo exit non-zero, so it always surfaces as a
	// RemoteCommandError carrying the denial line. Require that error as well as
	// the line match: a command that merely prints the line and SUCCEEDS
	// (err == nil) must not trigger a step-up and a re-run of a side-effecting
	// command.
	var remoteErr *event.RemoteCommandError
	if !errors.As(err, &remoteErr) || !hasSudoPresenceDenial(remoteErr.Output) {
		return err
	}

	if !utils.IsInteractiveShell() {
		// Non-interactive: surface the verification link so a human reading the
		// logs can complete MFA out of band, then re-run. We cannot prompt or
		// open a browser here.
		if url, linkErr := mfa.GetMFALinkForSudo(ac, serverName); linkErr == nil && url != "" {
			utils.CliInfo("MFA verification link (open in a browser):\n  %s", url)
		}
		return err
	}

	if stepErr := mfa.StepUpForSudo(ac, serverName); stepErr != nil {
		utils.CliWarning("MFA step-up did not complete: %s", stepErr)
		return err
	}

	// Presence is fresh—retry once. Any remaining denial falls through to the
	// static hint in HandleCommandResult.
	return RunCommandWithRetry(ac, serverName, command, username, groupname, env, workSessionID, out)
}

// isApprovalDenial reports whether err is a SUDO_APPROVAL_REQUIRED denial: the
// command exited non-zero (a real denial always surfaces as a
// RemoteCommandError) AND the plugin's exact approval denial line is present in
// the error's captured output. Requiring both guards against a command that
// merely prints the token but succeeds (err == nil) being mistaken for a
// pending approval.
func isApprovalDenial(err error) bool {
	var remoteErr *event.RemoteCommandError
	return errors.As(err, &remoteErr) && hasSudoApprovalDenial(remoteErr.Output)
}

// RunExecWithApprovalWait runs a command via RunExecWithPresenceStepUp and, when
// it is denied pending human approval (SUDO_APPROVAL_REQUIRED) and wait is set,
// blocks and re-attempts the command on a fixed interval until a reviewer
// approves it out of band (the re-run then succeeds or hits a different,
// terminal denial), or the bounded timeout elapses. When wait is false, or the
// denial is anything other than SUDO_APPROVAL_REQUIRED, it returns the first
// err unchanged so the caller's pending/denial handling runs.
//
// Re-attempting the command is the only poll available here: the plugin's denial
// line carries the denial code but no approval request id, and this credential
// channel has no approval-status endpoint to query (ADR 0015 moves approval out
// of band). Re-running is side-effect-safe: a sudo command pending approval is
// denied by the server and never executes, so each poll tick is a no-op denial
// until a reviewer approves, at which point the command runs exactly once. The
// poll mirrors the MFA step-up structure (api/mfa/mfa.go): a spinner, a
// fixed-interval ticker, and a precise deadline.
func RunExecWithApprovalWait(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, wait bool, out io.Writer) error {
	err := RunExecWithPresenceStepUp(ac, serverName, command, username, groupname, env, workSessionID, out)

	// Status-hold: the server parked this job at awaiting_approval (it never ran).
	// With --wait, resubscribe to the same job and stream once approved instead of
	// re-submitting; without --wait, surface it for HandlePendingApproval.
	var pendingErr *event.PendingApprovalError
	if errors.As(err, &pendingErr) {
		if !wait {
			return err
		}
		spinner := utils.NewSpinner("Waiting for approval in the Alpacon console (output streams once approved)...")
		spinner.Start()
		defer spinner.Stop()
		return event.StreamApprovedCommand(ac, pendingErr.CommandID, out, approvalWaitTimeout)
	}

	if !wait || !isApprovalDenial(err) {
		return err
	}

	spinner := utils.NewSpinner("Waiting for approval in the Alpacon console...")
	spinner.Start()

	timer := time.NewTimer(approvalWaitTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(approvalWaitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			spinner.Stop()
			// Return the last pending denial so the caller emits the standard
			// pending-approval signal and exit code.
			return err
		case <-ticker.C:
			// Re-attempt via the presence-aware path so a step-up still fires if
			// the approved command then needs fresh MFA (SUDO_PRESENCE_REQUIRED).
			err = RunExecWithPresenceStepUp(ac, serverName, command, username, groupname, env, workSessionID, out)
			// The server may switch this request from a denial-code to a status-hold
			// mid-wait; honor --wait by resuming the held job instead of exiting.
			if errors.As(err, &pendingErr) {
				spinner.Stop()
				return event.StreamApprovedCommand(ac, pendingErr.CommandID, out, approvalWaitTimeout)
			}
			if isApprovalDenial(err) {
				// Still pending—keep waiting.
				continue
			}
			spinner.Stop()
			return err
		}
	}
}

// HandlePendingApproval emits the structured pending-approval feedback for an
// exec sudo command that was denied SUDO_APPROVAL_REQUIRED and not waited on,
// then exits with ExitCodePendingApproval. It reports true when it handled the
// err; the caller skips its normal result handling on true. The exec denial line
// carries no approval request id, so the machine signal omits it. reRunHint is
// the exact command the caller invoked, so a human can copy-paste it once the
// request is approved.
func HandlePendingApproval(err error, reRunHint string) bool {
	// Status-hold: held job runs automatically once approved, so point at exec logs.
	var pendingErr *event.PendingApprovalError
	if errors.As(err, &pendingErr) {
		utils.PrintPendingApproval(
			"Approval required—this command is held for human approval in the Alpacon console (web). "+
				"It runs automatically once approved; pass --wait to block until then.",
			"", // the command detail carries no approval request id
			fmt.Sprintf("alpacon exec logs %s", pendingErr.CommandID),
		)
		os.Exit(utils.ExitCodePendingApproval)
		return true
	}
	if !isApprovalDenial(err) {
		return false
	}
	utils.PrintPendingApproval(
		"Approval required—a human must approve this sudo command in the Alpacon console (web). "+
			"Re-run after approval, or use --wait to block until it is approved.",
		"", // the exec sudo denial line carries no approval request id
		reRunHint,
	)
	os.Exit(utils.ExitCodePendingApproval)
	return true
}

// RunCommandWithRetry executes a remote command with MFA/username-required error
// handling and retry logic, streaming output to out. Used by exec and websh.
// workSessionID is forwarded as the work_session field; pass "" to omit it.
func RunCommandWithRetry(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, out io.Writer) error {
	err := event.RunCommandStreaming(ac, serverName, command, username, groupname, env, workSessionID, out)
	if propagated, ok := propagateCommandError(err); ok {
		return propagated
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
				return event.RunCommandStreaming(ac, serverName, command, username, groupname, env, workSessionID, out)
			},
		})
		// RetryOperation may surface a propagated error; re-check after HandleCommonErrors.
		if propagated, ok := propagateCommandError(err); ok {
			return propagated
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

// propagateCommandError reports errors RunCommandWithRetry must return unchanged
// (never MFA-retried or wrapped): phased errors and a status-hold PendingApprovalError.
func propagateCommandError(err error) (error, bool) {
	if phased, ok := asPhasedError(err); ok {
		return phased, true
	}
	var pending *event.PendingApprovalError
	if errors.As(err, &pending) {
		return pending, true
	}
	return nil, false
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
