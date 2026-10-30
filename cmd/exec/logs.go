package exec

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs JOB_ID",
	Short: "Fetch the result of a detached command",
	Long: `Fetch the result of a command submitted with --detach.

If the command is still running, prints the current status and exits.
Run the command again later to check for completion.`,
	Example: `  alpacon exec logs a1b2c3d4-5678-abcd-ef01-234567890abc`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		jobID := args[0]

		if !utils.IsUUID(jobID) {
			utils.CliUsageErrorWithExit("invalid JOB_ID %q: must be a UUID (e.g. a1b2c3d4-5678-abcd-ef01-234567890abc)", jobID)
			return
		}

		authMethod := config.ResolveAuthMethod()

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		details, err := event.GetCommandByID(alpaconClient, jobID)
		if err != nil {
			utils.HandleWorkSessionError(err, utils.OperationCommand, "", authMethod, "")
			utils.CliErrorWithExit("failed to fetch command result: %s", err)
			return
		}
		if line := event.StandingGrantLine(details); line != "" {
			fmt.Fprintln(os.Stderr, line)
		}

		// A parked command has no approval request, so this precedes the approval hold. This is the
		// detach path's only sight of the demand: SubmitCommand returns before the verdict.
		if event.IsAwaitingPurposeStatus(details.Status) {
			utils.PrintPurposeDemand(
				utils.PurposeDemandLead, details.ID, details.PurposeExpiresAt,
			)
			os.Exit(utils.ExitCodePurposeRequired)
		}

		// Exit 0 would read as a command that finished with nothing to show.
		if event.IsAwaitingApprovalStatus(details.Status) {
			utils.PrintPendingApproval(
				fmt.Sprintf("Approval required—this command is held for human approval in the Alpacon console (web); status %s. It runs automatically once approved.", details.Status),
				"", // the command detail carries no approval request id
				utils.NextAction{Command: fmt.Sprintf("alpacon exec logs %s", details.ID)},
			)
			os.Exit(utils.ExitCodePendingApproval)
		}

		// Exit 6 through the envelope, as exec and work-session create do: retrying only files
		// another approval request.
		if event.IsRejectedStatus(details.Status) {
			rejected := &event.CommandRejectedError{CommandID: details.ID, Cause: details.RejectionCause}
			// The gate refused a script body on the command line: the reviewer
			// sentence below would send the user to the wrong place.
			if rejected.Cause == event.RejectionCauseUseFileExecution {
				accountFlags, accountNote := logsAccountFlags(details)
				reportUseFileExecution(rejected, details.Server.Name, accountFlags, false, accountNote)
				return
			}
			utils.CliErrorEnvelopeWithExitCode(utils.ExitCodeNotApproved, "command", rejected,
				"command was rejected by a reviewer (status: %s)", details.Status)
			return
		}

		// Fetch output only for statuses whose outcome prints it. A status-only failure prints a
		// line of its own, so a hanging chunk store would only cost the wait; a running status
		// exits 0, so a read error would take the exitCode == 0 gate below and bury the "still
		// running" line.
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
		// output. Sanitized here: text one principal wrote now reaches another principal's
		// terminal.
		if details.Purpose != "" {
			fmt.Fprintf(os.Stderr, "Stated purpose: %s\n",
				utils.SanitizeTerminalText(details.Purpose))
		}

		stdoutLine, stderrLine, exitCode := logsCommandOutcome(details)
		// The command's own failure carries the remote exit code this command
		// propagates, so it outranks an unreadable output—the order
		// runCommandFallbackFromID keeps on the polling path.
		if exitCode == 0 && readErr != nil {
			utils.CliErrorWithExit("failed to read command output: %s", readErr)
			return
		}
		if stdoutLine != "" {
			fmt.Println(stdoutLine)
		}
		if stderrLine != "" {
			fmt.Fprint(os.Stderr, stderrLine)
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	},
}

func init() {
	ExecCmd.AddCommand(logsCmd)
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
// awaiting_purpose demand, the awaiting_approval hold, nor a rejection reaches
// here: the caller answers all three on their own contracts first.
func logsCommandOutcome(details event.EventDetails) (stdoutLine, stderrLine string, exitCode int) {
	// These lines are written raw, outside the Cli* helpers. A Status that reached a branch equals
	// the literal it was compared against and needs no strip; the ID, an unknown phase and the
	// unrecognized-status fallback carry the server's own text and do.
	if event.IsRunningStatus(details.Status) {
		stderrLine = fmt.Sprintf(
			"command is still running (status: %s).\nRun `alpacon exec logs %s` again to check later.\n",
			details.Status, utils.SanitizeTerminalText(details.ID),
		)
		return "", stderrLine, 0
	}

	if event.IsStatusOnlyFailure(details.Status) {
		if details.ErrorPhase != nil && *details.ErrorPhase != "" {
			phase, desc := sanitizedPhaseParts(*details.ErrorPhase)
			stderrLine = fmt.Sprintf("%s: [%s] %s (status=%s)\n",
				utils.Red("Error"), phase, desc, details.Status) +
				phaseHintLine(*details.ErrorPhase)
		} else {
			stderrLine = fmt.Sprintf("%s: command failed with status: %s\n",
				utils.Red("Error"), details.Status)
		}
		return "", stderrLine, 1
	}

	if details.Success != nil && !*details.Success {
		exitCode = 1
		if details.ExitCode != nil {
			exitCode = *details.ExitCode
		}
		if details.ErrorPhase != nil && *details.ErrorPhase != "" {
			phase, desc := sanitizedPhaseParts(*details.ErrorPhase)
			stderrLine = fmt.Sprintf("%s: [%s] %s\n", utils.Red("Error"), phase, desc) +
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
		utils.Red("Error"), utils.SanitizeTerminalText(details.Status))
	return details.Result, stderrLine, 1
}
