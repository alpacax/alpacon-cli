package exec

import (
	"errors"
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
)

// HandleCommandResult exits appropriately on error. Output is streamed to stdout
// during execution; on a remote failure the error carries that output, used here
// only to surface the sudo-denial hint (not re-printed). invokedAs names the
// command the user ran so a hint can quote it; empty falls back to exec.
func HandleCommandResult(err error, invokedAs Invocation) {
	if err != nil {
		var remoteErr *event.RemoteCommandError
		if errors.As(err, &remoteErr) {
			stderrText, exitCode := remoteFailureReport(remoteErr)
			fmt.Fprint(os.Stderr, stderrText)
			os.Exit(exitCode)
		}
		var clientTimeout *event.ClientTimeoutError
		if errors.As(err, &clientTimeout) {
			fmt.Fprint(os.Stderr, clientTimeoutLine())
			// A wait that ended on a refusal (a 429 in practice) keeps its hint.
			utils.PrintRefusalHints(os.Stderr, clientTimeout)
			os.Exit(1)
		}
		var rejected *event.CommandRejectedError
		if errors.As(err, &rejected) {
			// Settled without a grant: retrying only files another approval request.
			utils.CliErrorEnvelopeWithExitCode(utils.ExitCodeNotApproved, "command", err, "%s", rejected)
			return
		}
		if isCommandInlineCredentialError(err) {
			reportCodedRefusal("command", err, commandInlineCredentialMessage, credentialInlineHint(invokedAs))
			return
		}
		utils.CliErrorWithExit("%s", err)
	}
}

// remoteFailureReport is what HandleCommandResult writes to stderr for a
// command that failed on the server, and the exit code it leaves with: the
// outcome line, then the sudo denial guidance when the output needs one.
func remoteFailureReport(remoteErr *event.RemoteCommandError) (string, int) {
	stderrLine, exitCode := remoteCommandOutcome(remoteErr)
	return stderrLine + sudoDenialHint(remoteErr.Output), exitCode
}

// reportCodedRefusal is the one way out for a server refusal the CLI has a
// sentence for: under --output json the envelope carries the server's code and
// the message; table mode prints the red Error line and the hint. Exits 1.
// The server's hint, when err carries one, takes the place of the local hint.
func reportCodedRefusal(operation string, err error, message, hint string) {
	reportCodedRefusalWithExit(1, operation, err, message, hint, nil)
}

// reportCodedRefusalWithExit is reportCodedRefusal with a chosen exit code and
// the next_actions the JSON envelope carries; table mode shows only the hint,
// so it should spell out the same actions for a person. Under --output json an
// ErrInvalidID refusal goes out as a usage error with exit 2, so the envelope's
// code and the exit code agree.
func reportCodedRefusalWithExit(code int, operation string, err error, message, hint string, actions []utils.NextAction) {
	if utils.OutputFormat == utils.OutputFormatJSON && errors.Is(err, utils.ErrInvalidID) {
		utils.CliUsageErrorEnvelopeWithExit(operation, "%s.", message)
		return
	}
	utils.ExitWithFailureJSON(utils.Failure{ExitCode: code, Operation: operation, Err: err, Message: message + ".", NextActions: actions})
	fmt.Fprintf(os.Stderr, "%s: %s.\n", utils.Red("Error"), message)
	if hint := codedRefusalHint(err, hint); hint != "" {
		fmt.Fprint(os.Stderr, hint)
	}
	os.Exit(code)
}

// codedRefusalHint is the hint reportCodedRefusal prints: the server's, when
// err carries one, otherwise the local one it was given.
func codedRefusalHint(err error, local string) string {
	if serverHint := utils.RefusalHint(err); serverHint != "" {
		return denialHintLine(serverHint + "\n")
	}
	return local
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
	// The purpose demand is the caller's to answer, so it must reach
	// HandlePurposeDemand intact rather than be retried as a transport failure.
	var purposeDemand *event.AwaitingPurposeError
	if errors.As(err, &purposeDemand) {
		return purposeDemand, true
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

// detachResultLines renders the --detach confirmation. The job id comes from the
// server's submit response and both lines are written raw, outside the Cli*
// helpers—sanitize here (#364).
func detachResultLines(jobID string) (string, string) {
	jobID = utils.SanitizeTerminalText(jobID)
	return fmt.Sprintf("Job submitted: %s", jobID),
		fmt.Sprintf("Run `alpacon exec logs %s` to check the result.", jobID)
}

// sanitizedPhaseParts returns the phase identifier and its description ready for
// a raw stderr write, outside the Cli* sanitizing helpers (#364). The identifier
// is always the server's; the description is a constant from phaseDescriptions
// for a known phase, and the raw phase echoed back for an unknown one. Describing
// before stripping is what keeps a payload that only becomes a known phase once
// sanitized from borrowing that phase's description.
func sanitizedPhaseParts(phase string) (id, desc string) {
	return utils.SanitizeTerminalText(phase),
		utils.SanitizeTerminalText(event.DescribePhase(phase))
}

// phaseHintLine returns the indented hint line for a phase, including its
// trailing newline, or "" when the phase has no hint.
func phaseHintLine(phase string) string {
	hint := event.PhaseHint(phase)
	if hint == "" {
		return ""
	}
	return fmt.Sprintf("Hint: %s\n", utils.SanitizeTerminalText(hint))
}

// remoteCommandOutcome renders the stderr phase line and exit code for a remote
// command failure. The command's stdout was already streamed during execution,
// so it is not re-emitted here. stderrLine already includes its trailing newline.
func remoteCommandOutcome(remoteErr *event.RemoteCommandError) (stderrLine string, exitCode int) {
	if remoteErr.ErrorPhase != "" {
		phase, desc := sanitizedPhaseParts(remoteErr.ErrorPhase)
		stderrLine = fmt.Sprintf("%s: [%s] %s\n", utils.Red("Error"), phase, desc) +
			phaseHintLine(remoteErr.ErrorPhase)
	}
	return stderrLine, remoteErr.ExitCode
}

// HandleExecUserRefusal reports the missing execution user and exits 1, or
// returns false when err is something else. Under --output json it returns
// false so the envelope stays what it is today.
func HandleExecUserRefusal(err error, parsed RemoteExecArgs) bool {
	if utils.OutputFormat == utils.OutputFormatJSON {
		return false
	}
	message, hint, ok := execUserRefusal(err, parsed)
	if !ok {
		return false
	}
	reportCodedRefusal("command", err, message, hint)
	return true
}

// execUserRefusal renders the refusal for a command sent with no execution user.
// One that named a user or group keeps the server's own error, which this hint would contradict.
func execUserRefusal(err error, parsed RemoteExecArgs) (message, hint string, ok bool) {
	if parsed.Username != "" || parsed.Groupname != "" ||
		utils.StructuredErrorCode(err) != usernameGroupnameInvalidCode {
		return "", "", false
	}
	return fmt.Sprintf("no execution user was resolved for '%s' server", utils.SanitizeTerminalText(parsed.Server)),
		denialHintLine(fmt.Sprintf(execUserRequiredHint, execUserRequiredExamples(parsed.InvokedAs))), true
}

func execUserRequiredExamples(invokedAs Invocation) string {
	if invokedAs == WebshInvocation {
		return "  alpacon websh -u USERNAME SERVER 'COMMAND'\n  alpacon websh USERNAME@SERVER 'COMMAND'\n"
	}
	return "  alpacon exec -u USERNAME SERVER -- COMMAND\n  alpacon exec USERNAME@SERVER -- COMMAND\n"
}
