package exec

import (
	"fmt"
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var purposeCmd = &cobra.Command{
	Use:   "purpose JOB_ID PURPOSE",
	Short: "State what a held command is for, and send it back for judgment",
	Long: `State what a held command is for, and send it back for judgment.

When a command an agent submitted draws a verdict that would queue it for a
human, the server holds it and asks what the command is for instead of opening
an approval request. Nobody has been notified while the demand is open: the
answer is yours to give. This states it, and the assessor then judges the
command once more with the purpose in hand. Whatever that second verdict is—run,
hold for a human, or deny—is reached by exactly the path an unheld command takes.

There is one demand per command, and it expires about a minute after it is
issued. On silence the command is not blocked; it takes the ordinary path, and
the chance to explain it is gone. A late or second answer is refused.

Only the principal that submitted the command may answer. The card and the
assessor both read this text as the requester's own statement, so someone else
writing it would make the record false.

Write a purpose that states a fact local to this host which the work session's
description does not already imply—clock skew against a certificate's notBefore
window, a duplicate config block overriding the edited value, contention for a
single JVM attach slot. General knowledge adds nothing the assessor does not
already have. A purpose cannot lower a command's intrinsic risk, cannot outrank
the session description, and cannot make an unmeasurable command measurable; an
attempt to argue the verdict down is reported in its own right and denied.

Prefer stating it up front with 'alpacon exec --purpose': the assessor then
judges on the first pass and no demand is issued at all.

Exit code 1 covers every refusal. A settled command and an answer from the wrong
principal share one server error, so this cannot report which happened; read the
command's state with 'alpacon exec logs JOB_ID' rather than re-submitting it.`,
	Example: `  # Answer a demand reported by 'alpacon exec' (exit code 7)
  alpacon exec purpose a1b2c3d4-5678-abcd-ef01-234567890abc \
    'The host clock is 40s ahead, so the renewed cert reads as not-yet-valid.'

  # State it up front instead, and skip the demand entirely
  alpacon exec --purpose 'chronyd drifted; the cert reads as future-dated' \
    prod-web -- systemctl restart chronyd`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		jobID, purpose := args[0], args[1]

		if !utils.IsUUID(jobID) {
			utils.CliErrorWithExitCode(utils.ExitCodeUsageError, "invalid JOB_ID %q: must be a UUID (e.g. a1b2c3d4-5678-abcd-ef01-234567890abc)", jobID)
			return
		}
		if strings.TrimSpace(purpose) == "" {
			utils.CliErrorWithExitCode(utils.ExitCodeUsageError, "PURPOSE cannot be empty: the server refuses a blank answer and the command only gets one demand")
			return
		}
		if len(purpose) > PurposeMaxLength {
			utils.CliErrorWithExitCode(utils.ExitCodeUsageError, "PURPOSE is limited to %d characters; the server refuses a longer one", PurposeMaxLength)
			return
		}

		authMethod := config.ResolveAuthMethod()

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		if err = event.AnswerPurposeDemand(alpaconClient, jobID, purpose); err != nil {
			utils.HandleWorkSessionError(err, "command", "", authMethod, "")
			utils.CliErrorWithExit(
				"failed to state the purpose for %s: %s. The demand may have expired, it may already have been answered, "+
					"or this credential may not be the one that submitted the command. Read the command's state with "+
					"`alpacon exec logs %s`—do not re-submit it, which would create a second command.",
				jobID, err, jobID,
			)
			return
		}

		// The command re-enters judgment on the worker, so there is no verdict to
		// report here; naming where to read it keeps the caller off a resubmit.
		fmt.Fprintf(os.Stderr, "Purpose recorded. The command is being judged again with it in hand.\n")
		fmt.Fprintf(os.Stderr, "  alpacon exec logs %s  # read the outcome\n", utils.SanitizeTerminalText(jobID))
	},
}

func init() {
	ExecCmd.AddCommand(purposeCmd)
}
