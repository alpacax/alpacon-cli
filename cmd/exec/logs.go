package exec

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var LogsCmd = &cobra.Command{
	Use:   "logs JOB_ID",
	Short: "Fetch the result of a detached command",
	Long: `Fetch the result of a command submitted with --detach.

If the command is still running, prints the current status and exits.
Run the command again later to check for completion.`,
	Example: `  alpacon exec logs a1b2c3d4-5678-abcd-ef01-234567890abc`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		jobID := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		details, err := event.GetCommandByID(alpaconClient, jobID)
		if err != nil {
			utils.CliErrorWithExit("failed to fetch command result: %s", err)
			return
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
	ExecCmd.AddCommand(LogsCmd)
}

// logsCommandOutcome computes stdout line, stderr line, and exit code for
// a command result fetched via GetCommandByID. stderrLine ends with \n when non-empty.
// When Success is nil and status is terminal, the command is treated as successful —
// this mirrors the contract in RunCommand: alpamon sets success=(exitCode==0), so a
// nil success field on a terminal status means the server did not report a failure.
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
			stderrLine = fmt.Sprintf("%s: [%s] %s\n",
				utils.Red("Error"), *details.ErrorPhase, event.DescribePhase(*details.ErrorPhase))
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

	return details.Result, "", 0
}
