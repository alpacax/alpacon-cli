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

// sudoDenialLinePrefix is the exact terminal-facing denial line emitted by
// alpacon_approval.c via g_plugin_printf ("Alpacon denied this sudo command
// (CODE)."). The other "Permission denied (CODE)" form is assigned to *errstr,
// which only reaches the audit log—not the invoking terminal—so it must not be
// matched. Anchoring on this full prefix (not a bare "(CODE)") stops a command
// whose own output prints "(SUDO_RISK_DENIED)" from forging a hint.
const sudoDenialLinePrefix = "Alpacon denied this sudo command"

// sudoPresenceRequiredCode is the one denial code the CLI resolves in-flow (an
// MFA step-up). The hint table and hasSudoPresenceDenial both name it from
// here: editing one alone would silently stop the step-up.
const sudoPresenceRequiredCode = "SUDO_PRESENCE_REQUIRED"

// commandInlineCredentialMessage is the exec-facing error line for the
// alpacon-server inline-credential gate (utils.CommandInlineCredential, ADR 0037).
const commandInlineCredentialMessage = "server rejected this command—the command line carries a credential"

// ExecInvocation and WebshInvocation are the two Invocation values (see the
// Invocation type below).
const (
	ExecInvocation  Invocation = "alpacon exec"
	WebshInvocation Invocation = "alpacon websh"
)

// approvalWaitPollInterval throttles the --wait re-attempt loop—slower than the MFA poll (api/mfa/mfa.go) since each tick re-runs the command; a var so tests can shorten it.
var approvalWaitPollInterval = 5 * time.Second

// Test seams so a unit test can drive the deadline/resume logic without real network I/O.
var (
	runPresenceStepUp     = RunExecWithPresenceStepUp
	streamApprovedCommand = event.StreamApprovedCommand
)

// sudoDenialHints maps a non-interactive sudo denial code to actionable
// guidance. Codes are kept in sync with alpacon-server utils/error_codes.py.
//
// The codes are UPPERCASE because alpacon_approval.c only passes [A-Z0-9_]
// codes through its sanitizer into the user-facing denial line (lowercase
// values are dropped). Each hint stays at the denial *category* level (what to
// do)—the server never sends the risk score or reasoning to a client.
//
// pendingApproval marks the codes the server emits after creating an approval
// grant: the sudo call still fails now (an interactive sudo cannot wait on an
// out-of-band approval, ADR 0016 §3), but a reviewer can still approve it.
// Flagging them here rather than in a second list is what keeps the hints and
// that code set from drifting apart.
var sudoDenialHints = []struct {
	code, guidance  string
	pendingApproval bool
}{
	{
		code: "SUDO_NO_WORKSESSION_POLICY",
		guidance: "sudo was denied: this command is not covered by an MFA-bypass policy in your work session.\n" +
			"Add it and re-run (omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --sudo \"<command>\"\n",
	},
	{
		// An MFA step-up does not resolve this one—only a policy that bypasses
		// MFA does—so it stays out of the step-up path.
		code: "SUDO_POLICY_MFA_REQUIRED",
		guidance: "sudo was denied: a policy covers this command but requires MFA, which a non-interactive command cannot complete.\n" +
			"Cover it with an MFA-bypass policy and re-run (omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --sudo \"<command>\"\n",
	},
	{
		code:     sudoPresenceRequiredCode,
		guidance: "sudo needs a recent MFA: complete a step-up, then re-run the command.\n",
	},
	{
		code:            "SUDO_APPROVAL_REQUIRED",
		guidance:        "sudo needs approval: an approval request was created. Re-run after a reviewer approves it.\n",
		pendingApproval: true,
	},
	{
		// The session title and description both ride in the risk payload the
		// judge reads, but only the title is a way around the wait (ADR 0016
		// §4-5): a description edit on an approved/active session is queued for
		// an approval of its own (work_sessions/services.py
		// compute_modification_split), which is the very wait this path skips.
		code: "SUDO_INTENT_DEVIATION",
		guidance: "sudo needs approval: this command reads as off-purpose for your work session, so an approval request was created.\n" +
			"If the session's stated purpose is out of date, re-declare it and re-run instead of waiting for a reviewer (omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --title \"<what you are doing>\"\n" +
			"Editing the description instead is not a way around the wait: on an approved or active session that edit queues its own approval.\n",
		pendingApproval: true,
	},
	{
		code:     "SUDO_RISK_DENIED",
		guidance: "sudo was denied by runtime risk assessment; this command is not permitted in this work session.\n",
	},
}

// Invocation names the command the user ran, so a hint can show an example
// they can copy without translating it first. A defined type rather than a
// bare string, so a stray value cannot be assigned by accident. Websh command
// mode reaches this package through RemoteExecArgs.InvokedAs; the zero value
// renders as exec.
type Invocation string

// denialCodePresent reports whether output contains the plugin's terminal denial
// line for the given code. It anchors on the full "Alpacon denied this sudo
// command (CODE)." line—including the trailing period the plugin emits—never a
// bare "(CODE)" token, so a command whose own output prints the token cannot
// forge a match on a command that succeeded. Every detector routes through here
// so the anchoring logic lives in one place.
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

// isCommandInlineCredentialError reports whether err carries the alpacon-server
// inline-credential gate code (utils.CommandInlineCredential, ADR 0037): the
// submitted command line itself contained a credential (e.g. a -p/--password
// flag, a KEY=VALUE secret such as PGPASSWORD=..., or a user:pass@host
// connection string), so the server refused the command before it ever ran
// rather than persist that line.
func isCommandInlineCredentialError(err error) bool {
	code, _ := utils.ParseErrorResponse(err)
	return code == utils.CommandInlineCredential
}

// credentialInlineExample renders the --env line for invokedAs. The two commands
// take the remote command differently: only exec has the -- separator
// (cmd/exec/parse.go), while websh takes it as one quoted argument.
func credentialInlineExample(invokedAs Invocation) string {
	if invokedAs == WebshInvocation {
		return string(WebshInvocation) + ` --env="SECRET_NAME" db-server '<command>'`
	}
	return string(ExecInvocation) + ` --env="SECRET_NAME" db-server -- <command>`
}

// credentialInlineHint returns the actionable guidance printed alongside
// commandInlineCredentialMessage. It never echoes the rejected command
// line—only fixed guidance naming --env—so it cannot leak the credential it is
// warning about. invokedAs picks the example; empty falls back to exec.
func credentialInlineHint(invokedAs Invocation) string {
	return fmt.Sprintf(
		"%s move the secret to --env instead (its value is read from your shell, so it never lands on the command line the server stores):\n"+
			"  %s\n",
		utils.Yellow("Hint:"), credentialInlineExample(invokedAs))
}

// hasSudoPresenceDenial reports whether output carries the non-interactive sudo
// presence denial (SUDO_PRESENCE_REQUIRED), the only denial the CLI can resolve
// in-flow via an MFA step-up.
func hasSudoPresenceDenial(output string) bool {
	return denialCodePresent(output, sudoPresenceRequiredCode)
}

// hasSudoApprovalDenial reports whether output carries a denial that left an
// approval request in flight (the pendingApproval codes in sudoDenialHints): a
// human must approve it out of band in the Alpacon console before the command
// can run. Like the other detectors it anchors on the plugin's exact terminal
// denial line via denialCodePresent, so a command that merely prints the token
// in its own output cannot forge a pending signal or wedge --wait into an
// indefinite re-run loop.
func hasSudoApprovalDenial(output string) bool {
	for _, h := range sudoDenialHints {
		if h.pendingApproval && denialCodePresent(output, h.code) {
			return true
		}
	}
	return false
}

// RunExecWithPresenceStepUp runs a command via RunCommandWithRetry and, when it
// is denied for a missing recent MFA (SUDO_PRESENCE_REQUIRED) on an interactive
// terminal, offers an MFA step-up and retries once. Non-interactive callers
// (scripts, CI, AI agents) fall through unchanged so HandleCommandResult prints
// the static denial hint; non-interactive humans additionally get the
// verification link they can complete out of band. Reached via RunRemoteExec by
// exec and websh command mode; interactive websh keeps its own sudo MFA flow.
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

// approvalDenialOutput returns the captured output of a denial that left an
// approval request in flight: the command exited non-zero (a real denial always
// surfaces as a RemoteCommandError) AND the plugin's exact denial line for a
// pendingApproval code is in that output. Requiring both keeps a command that
// merely prints the token but succeeds (err == nil) from being mistaken for a
// pending approval. It hands back the output, not a bool, so a caller needing
// the denial code does not restate the rule for what counts as one.
func approvalDenialOutput(err error) (string, bool) {
	var remoteErr *event.RemoteCommandError
	if !errors.As(err, &remoteErr) || !hasSudoApprovalDenial(remoteErr.Output) {
		return "", false
	}
	return remoteErr.Output, true
}

// isApprovalDenial reports whether err is a denial with an approval request in flight.
func isApprovalDenial(err error) bool {
	_, ok := approvalDenialOutput(err)
	return ok
}

// RunExecWithApprovalWait runs a command via RunExecWithPresenceStepUp and, when
// it is denied with an approval request in flight (a pendingApproval code) and
// waitTimeout is positive, blocks and re-attempts the command on a fixed
// interval until a reviewer approves it out of band (the re-run then succeeds or
// hits a different, terminal denial), or the bounded timeout elapses. When
// waitTimeout is zero or negative, or the denial carries a terminal code, it
// returns the first err unchanged so the caller's pending/denial handling runs.
//
// Re-attempting the command is the only poll available here: the plugin's denial
// line carries the denial code but no approval request id, and this credential
// channel has no approval-status endpoint to query (ADR 0015 moves approval out
// of band). Re-running is side-effect-safe: a sudo command pending approval is
// denied by the server and never executes, so each poll tick is a no-op denial
// until a reviewer approves, at which point the command runs exactly once. The
// poll mirrors the MFA step-up structure (api/mfa/mfa.go): a spinner, a
// fixed-interval ticker, and a precise deadline.
func RunExecWithApprovalWait(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID string, waitTimeout time.Duration, out io.Writer) error {
	err := runPresenceStepUp(ac, serverName, command, username, groupname, env, workSessionID, out)

	// Status-hold: the server parked this job at awaiting_approval (it never ran).
	// With --wait, resubscribe to the same job and stream once approved instead of
	// re-submitting; without --wait, surface it for HandlePendingApproval.
	var pendingErr *event.PendingApprovalError
	if errors.As(err, &pendingErr) {
		if waitTimeout <= 0 {
			return err
		}
		spinner := utils.NewSpinner("Waiting for approval in the Alpacon console (output streams once approved)...")
		spinner.Start()
		defer spinner.Stop()
		return streamApprovedCommand(ac, pendingErr.CommandID, out, waitTimeout)
	}

	if waitTimeout <= 0 || !isApprovalDenial(err) {
		return err
	}

	spinner := utils.NewSpinner("Waiting for approval in the Alpacon console...")
	spinner.Start()

	deadline := time.Now().Add(waitTimeout)
	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(approvalWaitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			spinner.Stop()
			// Report only the timeout; the caller's pending-approval message already names --wait-approval.
			utils.CliWarning("Approval wait timed out after %s; the command is still pending.", waitTimeout)
			return err
		case <-ticker.C:
			// Re-attempt via the presence-aware path so a step-up still fires if
			// the approved command then needs fresh MFA (SUDO_PRESENCE_REQUIRED).
			err = runPresenceStepUp(ac, serverName, command, username, groupname, env, workSessionID, out)
			// The server may switch this request from a denial-code to a status-hold
			// mid-wait; honor --wait by resuming the held job instead of exiting.
			if errors.As(err, &pendingErr) {
				spinner.Stop()
				// Resume inside the original window: this loop already consumed part
				// of it, so re-arming with the full timeout would double the wait.
				remaining := time.Until(deadline)
				if remaining <= 0 {
					return err
				}
				return streamApprovedCommand(ac, pendingErr.CommandID, out, remaining)
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
// exec sudo command denied with an approval request in flight and not waited on,
// then exits with ExitCodePendingApproval. It reports true when it handled the
// err; the caller skips its normal result handling on true. The exec denial line
// carries no approval request id, so the machine signal omits it. reRunHint is
// the exact command the caller invoked (with any --env caveat in its
// Description), so a human can copy-paste it once the request is approved.
func HandlePendingApproval(err error, reRunHint utils.NextAction) bool {
	// Status-hold: held job runs automatically once approved, so point at exec logs.
	var pendingErr *event.PendingApprovalError
	if errors.As(err, &pendingErr) {
		utils.PrintPendingApproval(
			"Approval required—this command is held for human approval in the Alpacon console (web). "+
				"It runs automatically once approved; pass --wait (or --wait-approval DURATION for a longer wait) to block until then.",
			"", // the command detail carries no approval request id
			utils.NextAction{Command: fmt.Sprintf("alpacon exec logs %s", pendingErr.CommandID)},
		)
		os.Exit(utils.ExitCodePendingApproval)
		return true
	}
	output, ok := approvalDenialOutput(err)
	if !ok {
		return false
	}
	// This exits before HandleCommandResult, the hint's only other caller. The
	// pending message covers re-running after approval; only the hint carries
	// the self-service path out (re-declaring a stale session intent).
	if hint := sudoDenialHint(output); hint != "" {
		fmt.Fprint(os.Stderr, hint)
	}
	utils.PrintPendingApproval(
		"Approval required—a human must approve this sudo command in the Alpacon console (web). "+
			"Re-run after approval, or use --wait (or --wait-approval DURATION for a longer wait) to block until it is approved.",
		"", // the exec sudo denial line carries no approval request id
		reRunHint,
	)
	os.Exit(utils.ExitCodePendingApproval)
	return true
}

// RunCommandWithRetry executes a remote command with MFA/username-required error
// handling and retry logic, streaming output to out.
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
// only to surface the sudo-denial hint (not re-printed). invokedAs names the
// command the user ran so a hint can quote it; empty falls back to exec.
func HandleCommandResult(err error, invokedAs Invocation) {
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
		if isCommandInlineCredentialError(err) {
			if utils.OutputFormat == utils.OutputFormatJSON {
				utils.CliErrorEnvelopeWithExit("command", err, "%s.", commandInlineCredentialMessage)
				return
			}
			fmt.Fprintf(os.Stderr, "%s: %s.\n", utils.Red("Error"), commandInlineCredentialMessage)
			fmt.Fprint(os.Stderr, credentialInlineHint(invokedAs))
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
