package exec

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// logsStatusRunning/logsStatusSucceeded/logsStatusFailed/logsStatusUnknown are
// the remaining `status` values of the stable `exec logs --output json`
// contract that have no shared constant elsewhere. pending_approval and
// rejected reuse utils.PendingApprovalStatus/utils.RejectedStatus so the same
// string is never spelled two different ways.
const (
	logsStatusRunning   = "running"
	logsStatusSucceeded = "succeeded"
	logsStatusFailed    = "failed"
	logsStatusUnknown   = "unknown"
)

// ansiSGR matches ANSI SGR (Select Graphic Rendition) escape sequences—the only
// escape codes utils.Red/Yellow/Bold ever emit—so the JSON envelope's message
// field is clean regardless of whether the process happened to detect a
// terminal on stderr.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

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

		// Output is stored as chunks (Result is empty under the streaming
		// contract); reconstruct it, falling back to Result for legacy commands.
		if output, oerr := event.GetCommandOutput(alpaconClient, jobID); oerr == nil && output != "" {
			details.Result = output
		}

		stdoutLine, stderrLine, exitCode := logsCommandOutcome(details)

		if utils.OutputFormat == utils.OutputFormatJSON {
			envelope := buildLogsJSON(details, stdoutLine, stderrLine, exitCode)
			if err := utils.PrintJSONValue(os.Stdout, envelope); err != nil {
				utils.CliErrorWithExit("failed to marshal JSON: %s", err)
				return
			}
			if exitCode != 0 {
				os.Exit(exitCode)
			}
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

// logsJSONEnvelope is the JSON schema for `exec logs --output json`, documented
// in docs/superpowers/specs/253-unify-exit-code-contract.md section 3.4. Status
// is the stable public contract; ServerStatus is the raw server value, kept
// separate so the server adding new internal states doesn't change what a
// machine consumer branches on.
type logsJSONEnvelope struct {
	OK           bool     `json:"ok"`
	Status       string   `json:"status"`
	ServerStatus string   `json:"server_status"`
	ExitCode     int      `json:"exit_code"`
	ErrorCode    string   `json:"error_code,omitempty"`
	ErrorPhase   string   `json:"error_phase,omitempty"`
	Message      string   `json:"message"`
	Result       string   `json:"result,omitempty"`
	JobID        string   `json:"job_id"`
	NextActions  []string `json:"next_actions,omitempty"`
}

func init() {
	ExecCmd.AddCommand(logsCmd)
}

// logsCommandOutcome maps GetCommandByID details to (stdout, stderr, exitCode). If non-empty, stderrLine ends with \n.
func logsCommandOutcome(details event.EventDetails) (stdoutLine, stderrLine string, exitCode int) {
	if event.IsRunningStatus(details.Status) {
		stderrLine = fmt.Sprintf(
			"command is still running (status: %s).\nRun `alpacon exec logs %s` again to check later.\n",
			details.Status, details.ID,
		)
		return "", stderrLine, 0
	}

	// HITL: the command is held pending out-of-band human approval. It runs once
	// approved, so report it as in-progress (exit 0) and point at a later re-check
	// rather than treating it as an unrecognised terminal status.
	if event.IsAwaitingApprovalStatus(details.Status) {
		stderrLine = fmt.Sprintf(
			"command is awaiting approval (status: %s).\nIt runs once a reviewer approves it in the Alpacon console (web). Run `alpacon exec logs %s` again to check later.\n",
			details.Status, details.ID,
		)
		return "", stderrLine, 0
	}

	if details.Status == "rejected" {
		stderrLine = fmt.Sprintf(
			"%s: command was rejected by a reviewer (status: %s); do not retry, submit a new command if still needed\n",
			utils.Red("Error"), details.Status)
		return "", stderrLine, utils.ExitCodeCommandRejected
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

// classifyLogsStatus maps a raw server status (plus success/exit info) to the
// stable `status` value of the logs JSON contract (section 3.4.1 of the spec).
// The branch order mirrors logsCommandOutcome so the two never disagree on
// which group a status falls into.
func classifyLogsStatus(details event.EventDetails) string {
	switch {
	case event.IsRunningStatus(details.Status):
		return logsStatusRunning
	case event.IsAwaitingApprovalStatus(details.Status):
		return utils.PendingApprovalStatus
	case details.Status == "rejected":
		return utils.RejectedStatus
	case details.Status == "stuck" || details.Status == "error" || details.Status == "cancelled":
		return logsStatusFailed
	case details.Success != nil && !*details.Success:
		return logsStatusFailed
	case details.Success != nil:
		return logsStatusSucceeded
	case details.Status == "completed" || details.Status == "success":
		return logsStatusSucceeded
	default:
		return logsStatusUnknown
	}
}

// logsNextActions lists the actionable follow-up for a machine consumer polling
// `exec logs`. Only the two non-terminal statuses get one—there is nothing
// useful to suggest once a command has reached a terminal state.
func logsNextActions(status, jobID string) []string {
	switch status {
	case logsStatusRunning, utils.PendingApprovalStatus:
		return []string{fmt.Sprintf("Run `alpacon exec logs %s` again to check later.", jobID)}
	default:
		return nil
	}
}

// buildLogsJSON assembles the `exec logs --output json` envelope from the same
// (stdoutLine, stderrLine, exitCode) trio the plain-text path renders, so the
// two can never drift on exit code. ok is always true: this is a query command,
// and the query itself succeeded—status carries the underlying command's
// outcome (see spec section 3.4.2 for why this differs from exec's own
// pending-approval envelope, which uses ok:false).
func buildLogsJSON(details event.EventDetails, stdoutLine, stderrLine string, exitCode int) logsJSONEnvelope {
	status := classifyLogsStatus(details)

	envelope := logsJSONEnvelope{
		OK:           true,
		Status:       status,
		ServerStatus: details.Status,
		ExitCode:     exitCode,
		Message:      stripAnsi(strings.TrimRight(stderrLine, "\n")),
		Result:       stdoutLine,
		JobID:        details.ID,
		NextActions:  logsNextActions(status, details.ID),
	}
	if status == utils.RejectedStatus {
		envelope.ErrorCode = utils.RejectedErrorCode
	}
	if details.ErrorPhase != nil && *details.ErrorPhase != "" {
		envelope.ErrorPhase = *details.ErrorPhase
	}
	return envelope
}

// stripAnsi removes ANSI SGR escape codes from s.
func stripAnsi(s string) string {
	return ansiSGR.ReplaceAllString(s, "")
}
