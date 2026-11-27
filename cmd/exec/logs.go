package exec

import (
	"fmt"
	"io"

	"github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newLogsCommand(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:   "logs JOB_ID",
		Short: "Fetch the result of a detached command",
		Long: `Fetch the result of a command submitted with --detach.

If the command is still running, prints the current status and exits.
Run the command again later to check for completion.

A finished command's output goes to stdout as raw text, under --output json
too. Under --output json the outcomes that are not a result print a status
object on stdout instead: {"status":"running", ...} with exit 0 while the
command is still in progress, {"status":"pending_approval", ...} with exit 4,
and {"status":"purpose_required", ...} with exit 7. So exit 0 with a running
object means not finished, and exit 0 with anything else, an empty stdout
included, is the finished command's output—unless that output is itself such
an object. A command that failed with a remote exit status exits with it and ends
stderr with an error envelope carrying remote_exit_code. One that ends stuck,
error or cancelled, or reports no exit status, exits 1 with no
remote_exit_code.`,
		Example: `  alpacon exec logs 550e8400-e29b-41d4-a716-446655440000`,
		Args:    cobra.ExactArgs(1),
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			jobID := args[0]
			stdout, stderr := ios.Out, ios.ErrOut

			if !utils.IsUUID(jobID) {
				return utils.NewUsageExitError("invalid JOB_ID %q: must be a UUID (e.g. 550e8400-e29b-41d4-a716-446655440000)", jobID)
			}

			authMethod := config.ResolveAuthMethod()

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			details, err := event.GetCommandByID(alpaconClient, jobID)
			if err != nil {
				if wsErr := utils.WorkSessionExitError(err, utils.OperationCommand, "", authMethod, ""); wsErr != nil {
					return wsErr
				}
				return utils.NewExitError("failed to fetch command result: %s", err)
			}
			switch details.Status {
			case "completed", "success", "failed":
				if details.SudoGrantStatus != nil && *details.SudoGrantStatus == "pending_approval" {
					utils.EmitLocalSignal(localsignal.ApprovalPending, localsignal.Fields{
						Command: true, Argv0: localsignal.Argv0(details.Line), Server: details.Server.Name,
						Account: details.Username, Session: details.WorkSessionID(), ConsoleURL: details.ConsoleURL,
					}, alpaconClient)
				}
			}
			if line := event.StandingGrantLine(details); line != "" {
				_, _ = fmt.Fprintln(stderr, line)
			}

			// A parked command has no approval request, so this precedes the approval hold. This is the
			// detach path's only sight of the demand: SubmitCommand returns before the verdict.
			if event.IsAwaitingPurposeStatus(details.Status) {
				ios.PrintPurposeDemand(
					utils.PurposeDemandLead, jobID, details.PurposeExpiresAt,
				)
				return utils.NewSilentExitError(utils.ExitCodePurposeRequired)
			}

			// Exit 0 would read as a command that finished with nothing to show.
			if event.IsAwaitingApprovalStatus(details.Status) {
				assignee := details.ApprovalLane == event.ApprovalLaneAssignee
				fields := localsignal.Fields{Command: true, Argv0: localsignal.Argv0(details.Line), Server: details.Server.Name, Account: details.Username, ConsoleURL: details.ConsoleURL, Assignee: assignee}
				if details.WorkSession != nil {
					fields.Session = details.WorkSession.ID
				}
				utils.EmitLocalSignal(localsignal.ApprovalPending, fields, alpaconClient)
				requestID, lookupErr := approval.FindCommandApprovalRequestID(alpaconClient, jobID)
				if lookupErr != nil {
					ios.Warning("failed to read approval request: %s", lookupErr)
				}
				message := heldCommandMessage(assignee, requestID, details.Status)
				pending := &event.PendingApprovalError{
					WorkSessionPaused: details.WorkSessionPaused,
					WorkSessionID:     details.WorkSessionID(),
					WorkSessionStatus: details.WorkSessionStatus(),
					ConsoleURL:        details.ConsoleURL,
					ApprovalLane:      details.ApprovalLane,
				}
				message = withSessionResumeGuidance(message, pending)
				ios.PrintPendingApprovalWithError(
					message,
					requestID,
					pending,
					utils.NextAction{Command: fmt.Sprintf("alpacon exec logs %s", jobID)},
				)
				return utils.NewSilentExitError(utils.ExitCodePendingApproval)
			}

			// Exit 6 through the envelope, as exec does: a retry is refused again or
			// files another approval request.
			if details.IsRejected() {
				rejected := details.Rejection()
				utils.SignalRefusal(utils.WithSignalFields(rejected, localsignal.Fields{Command: true, Argv0: localsignal.Argv0(details.Line), Server: details.Server.Name, Account: details.Username}, alpaconClient))
				// A script-body refusal has file-execution guidance of its own.
				if rejected.WantsFileExecution() {
					accountFlags, accountNote := logsAccountFlags(details)
					return reportUseFileExecution(ios, stderr, rejected, details.Server.Name, accountFlags, false, accountNote)
				}
				rejected.CommandID = "" // the caller already named the job
				return commandRejectionFailure(ios, stderr, rejected, rejected)
			}

			// Fetch output only for statuses whose outcome prints it. A status-only failure prints a
			// line of its own, so a hanging chunk store would only cost the wait; a running status
			// prints no output, so the fetch would be wasted.
			var readErr error
			if !event.IsRunningStatus(details.Status) && !event.IsStatusOnlyFailure(details.Status) {
				output, oerr := event.ResolveCommandOutput(alpaconClient, jobID, details.Result)
				if oerr != nil {
					readErr = oerr
				} else {
					details.Result = output
				}
			}

			// The stated purpose goes to stderr ahead of the outcome, keeping stdout the command's
			// output. It is sanitized because text one principal wrote reaches another principal's
			// terminal.
			if details.Purpose != "" {
				_, _ = fmt.Fprintf(stderr, "Stated purpose: %s\n",
					utils.SanitizeTerminalText(details.Purpose))
			}

			if event.IsRunningStatus(details.Status) {
				if err := ios.PrintCommandRunning(jobID, details.Status); err != nil {
					return utils.NewExitError("%s", err)
				}
				return nil
			}

			stdoutLine, stderrLine, exitCode := logsCommandOutcome(ios, details)
			if exitCode == 0 && stderrLine == "" && details.PurposeExpiresAt != nil {
				stderrLine = fmt.Sprintf("command finished (status: %s).\n", utils.SanitizeTerminalText(details.Status))
			}
			// The command's own failure carries the remote exit code this command
			// propagates, so it outranks an unreadable output—the order
			// runCommandFallbackFromID keeps on the polling path.
			if exitCode == 0 && readErr != nil {
				return utils.NewExitError("failed to read command output: %s", readErr)
			}
			if stdoutLine != "" {
				_, _ = fmt.Fprintln(stdout, stdoutLine)
			}
			if exitCode == 0 {
				_, _ = fmt.Fprint(stderr, stderrLine)
				return nil
			}
			failure := func() utils.Failure { return logsFailure(details) }
			return utils.NewFailureExitErrorFunc(exitCode, failure, func(w io.Writer, _ *utils.IOStreams) {
				_, _ = fmt.Fprint(w, stderrLine)
			})
		}),
	}
}

// logsFailure is the --output json report of a failed command, matching logsCommandOutcome's branches.
func logsFailure(details event.EventDetails) utils.Failure {
	var phase string
	if details.ErrorPhase != nil {
		phase = *details.ErrorPhase
	}
	switch {
	case event.IsStatusOnlyFailure(details.Status):
		return remoteFailure(details.ExitCode, phase, details.Result, details.Status)
	case details.Success != nil:
		return remoteFailure(details.ExitCode, phase, details.Result, "" /* status */)
	}
	return utils.Failure{
		ErrorCode:      utils.ErrorCodeCLI,
		Message:        "the command ended with unrecognised status " + utils.SanitizeTerminalText(details.Status),
		Operation:      "command",
		RemoteExitCode: details.ExitCode,
	}
}

// heldCommandMessage words a held command for exec logs: the session assignee's
// own confirmation on the assignee lane, an approver's decision on any other.
func heldCommandMessage(assignee bool, requestID, status string) string {
	request := ""
	if requestID != "" {
		request = fmt.Sprintf(" (approval request: %s)", utils.SanitizeTerminalText(requestID))
	}
	if assignee {
		return fmt.Sprintf("Confirmation required—this command is awaiting your own confirmation as the session's assignee, not an approver's%s, in the Alpacon web console; status %s. It runs automatically once confirmed.", request, status)
	}
	return fmt.Sprintf("Approval required—this command is awaiting human approval%s in the Alpacon web console; status %s. It runs automatically once approved.", request, status)
}

// logsAccountFlags repeats the refused run's -u, -g and --work-session flags
// from the command detail. A detail with no username (an older server, or a
// field the viewer cannot see) gets an -u ACCOUNT placeholder and a note to
// fill it in: a command without -u would run as the server's default account.
// -g gets a placeholder too unless the detail names the group, since the
// original may have carried one.
func logsAccountFlags(details event.EventDetails) (flags []string, note string) {
	original := RemoteExecArgs{Username: details.Username, Groupname: details.Groupname}
	if details.WorkSession != nil {
		original.WorkSessionID = details.WorkSession.ID
	}
	flags = runAsFlags(original)
	if details.Username != "" {
		return flags, ""
	}
	// Bare words, not <account>: a shell reads angle brackets as redirections.
	placeholders := []string{"-u ACCOUNT"}
	note = "the command detail does not name the account the command ran as, so replace ACCOUNT with that account"
	if details.Groupname == "" {
		placeholders = append(placeholders, "-g GROUP")
		note += " and GROUP with its group, or drop -g if it had none"
	}
	note += ": without -u the command runs as the server's default account"
	return append(placeholders, flags...), note
}

// logsCommandOutcome guarantees a non-empty stderrLine ends with \n. Neither the
// awaiting_purpose demand, the awaiting_approval hold, a rejection, nor a running
// command reaches here: the caller answers all four on their own contracts first.
func logsCommandOutcome(ios *utils.IOStreams, details event.EventDetails) (stdoutLine, stderrLine string, exitCode int) {
	// These lines are written raw, outside the Cli* helpers. A Status that reached a branch equals
	// the literal it was compared against and needs no strip; an unknown phase and the
	// unrecognized-status fallback carry the server's own text and do.
	if event.IsStatusOnlyFailure(details.Status) {
		if details.ErrorPhase != nil && *details.ErrorPhase != "" {
			phase, desc := sanitizedPhaseParts(*details.ErrorPhase)
			stderrLine = fmt.Sprintf("%s: [%s] %s (status=%s)\n",
				ios.Red("Error"), phase, desc, details.Status) +
				phaseHintLine(*details.ErrorPhase)
		} else {
			stderrLine = fmt.Sprintf("%s: command failed with status: %s\n",
				ios.Red("Error"), details.Status)
		}
		return "", stderrLine, 1
	}

	if details.Success != nil && !*details.Success {
		exitCode = 1
		if details.ExitCode != nil {
			exitCode = utils.ProcessExitCode(*details.ExitCode)
		}
		if details.ErrorPhase != nil && *details.ErrorPhase != "" {
			phase, desc := sanitizedPhaseParts(*details.ErrorPhase)
			stderrLine = fmt.Sprintf("%s: [%s] %s\n", ios.Red("Error"), phase, desc) +
				phaseHintLine(*details.ErrorPhase)
		}
		return details.Result, stderrLine, exitCode
	}

	if details.Success != nil {
		return details.Result, "", 0
	}

	// A nil Success on a known-success status means Alpamon reported no failure: it sends
	// success=(exitCode==0).
	if details.Status == "completed" || details.Status == "success" {
		return details.Result, "", 0
	}

	stderrLine = fmt.Sprintf("%s: command ended with unrecognised status: %s\n",
		ios.Red("Error"), utils.SanitizeTerminalText(details.Status))
	return details.Result, stderrLine, 1
}
