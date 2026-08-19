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
			utils.CliErrorWithExit("invalid JOB_ID %q: must be a UUID (e.g. a1b2c3d4-5678-abcd-ef01-234567890abc)", jobID)
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
			utils.HandleWorkSessionError(err, "command", "", authMethod, "")
			utils.CliErrorWithExit("failed to fetch command result: %s", err)
			return
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

		// Exit 6 rather than 1, and through the envelope so --output json answers
		// this refusal the way exec and work-session create do: retrying only
		// files another approval request.
		if event.IsRejectedStatus(details.Status) {
			utils.CliErrorEnvelopeWithExitCode(utils.ExitCodeNotApproved, "command",
				&event.CommandRejectedError{CommandID: details.ID},
				"command was rejected by a reviewer (status: %s)", details.Status)
			return
		}

		// Output is stored as chunks (Result is empty under the streaming
		// contract); reconstruct it, falling back to Result for legacy commands.
		if output, oerr := event.GetCommandOutput(alpaconClient, jobID); oerr == nil && output != "" {
			details.Result = output
		}

		stdoutLine, stderrLine, exitCode := logsCommandOutcome(details)
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

// logsCommandOutcome guarantees a non-empty stderrLine ends with \n. Neither the
// awaiting_approval hold nor a rejection reaches here: the caller answers both on
// the approval contract first.
func logsCommandOutcome(details event.EventDetails) (stdoutLine, stderrLine string, exitCode int) {
	if event.IsRunningStatus(details.Status) {
		stderrLine = fmt.Sprintf(
			"command is still running (status: %s).\nRun `alpacon exec logs %s` again to check later.\n",
			details.Status, details.ID,
		)
		return "", stderrLine, 0
	}

	if details.Status == "stuck" || details.Status == "error" || details.Status == "cancelled" {
		if details.ErrorPhase != nil && *details.ErrorPhase != "" {
			stderrLine = fmt.Sprintf("%s: [%s] %s (status=%s)\n",
				utils.Red("Error"), *details.ErrorPhase, event.DescribePhase(*details.ErrorPhase), details.Status)
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
			stderrLine = fmt.Sprintf("%s: [%s] %s\n",
				utils.Red("Error"), *details.ErrorPhase, event.DescribePhase(*details.ErrorPhase))
		}
		return details.Result, stderrLine, exitCode
	}

	if details.Success != nil {
		return details.Result, "", 0
	}

	// Success nil on a known-success status means alpamon did not report failure (success=(exitCode==0) contract).
	if details.Status == "completed" || details.Status == "success" {
		return details.Result, "", 0
	}

	stderrLine = fmt.Sprintf("%s: command ended with unrecognised status: %s\n",
		utils.Red("Error"), details.Status)
	return details.Result, stderrLine, 1
}
