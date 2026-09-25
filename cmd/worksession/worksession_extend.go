package worksession

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	extendExpiresIn string
	extendExpiresAt string
	extendReason    string
)

var workSessionExtendCmd = &cobra.Command{
	Use:   "extend SESSION_ID",
	Short: "Extend the expiry of an approved or active work session",
	Long: `Extend the expiry of an approved or active work session.

--reason is required: a short justification an approver judges the request by.
Whether the extension applies immediately or waits for approval follows the
workspace's approval policy, the same rule as requesting a new session. When it
is queued for review the CLI reports that and exits (see "Exit codes" in the
README) rather than waiting. Check back with
'alpacon work-session describe SESSION_ID', or run 'extend' again with a new
reason once the request settles.`,
	Args: cobra.ExactArgs(1),
	Example: `  alpacon work-session extend ses-abc123 --expires-in 2h --reason "customer escalation, still triaging"
  alpacon work-session extend ses-abc123 --expires-at 2026-05-09T10:00:00Z --reason "customer escalation, still triaging"`,
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		expiresAtVal, err := parseExpiryFlag(extendExpiresIn, extendExpiresAt)
		if err != nil {
			if extendExpiresIn == "" && extendExpiresAt == "" {
				if !utils.IsInteractiveShell() {
					utils.CliUsageErrorEnvelopeWithExit(opExtend, "Non-interactive mode requires --expires-in or --expires-at.")
				}
				extendExpiresIn = utils.PromptForRequiredInput("Expires in (e.g. 1h, 2h, 4h): ")
				expiresAtVal, err = parseExpiryFlag(extendExpiresIn, "")
				if err != nil {
					utils.CliUsageErrorEnvelopeWithExit(opExtend, "Invalid expiry: %s.", err)
				}
			} else {
				utils.CliUsageErrorEnvelopeWithExit(opExtend, "Invalid expiry: %s.", err)
			}
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opExtend, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		req := wsapi.WorkSessionExtendRequest{ExpiresAt: expiresAtVal, Reason: strings.TrimSpace(extendReason)}
		session, status, err := wsapi.ExtendWorkSession(ac, id, req)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opExtend, err, "%s", extendErrorMessage(id, err))
		}

		// The status code is what tells 200 from 202 apart, not
		// PendingExtensionRequest on its own: a 200 body can still carry a
		// stale one (the workspace's approval policy changed to auto-approve
		// after the request was filed, or a lapsed request the periodic
		// sweep has not reached yet), and treating that as still-pending
		// would report an already-applied extension as unresolved.
		if status == http.StatusAccepted {
			printExtendPending(id, session.PendingExtensionRequest)
			os.Exit(utils.ExitCodePendingApproval)
		}

		printExtendSuccess(id, formatMutationExpiresAt(session.ExpiresAt))
	},
}

// printExtendSuccess writes the extended-session result: the mutation JSON
// envelope under --output json, a plain success line otherwise.
func printExtendSuccess(id, expiresAt string) {
	output := newWorkSessionExtendOutput(id, expiresAt)
	if utils.OutputFormat == utils.OutputFormatJSON {
		printWorkSessionMutationJSON(output)
		return
	}
	utils.CliSuccess("%s", output.Message)
}

// printExtendPending reports a 202: the extension was queued instead of
// applied. req is normally non-nil (the 202 contract always carries it), but
// this stays defensive rather than panic on a body that omits it. Next steps
// point at 'describe' to check status and a plain retry with a new reason—
// not 'work-session complete', which is unrelated to an extension decision.
func printExtendPending(id string, req *wsapi.PendingExtensionRequest) {
	message, requestID, requestedExpiresAt, deadline := extendPendingMessage(id, req)
	describeCmd := fmt.Sprintf("alpacon work-session describe %s", id)
	next := []utils.NextAction{
		{Command: describeCmd, Description: "check status; --output json shows pending_extension_request"},
		{Description: "retry 'work-session extend' with a new reason after the request settles"},
	}
	if utils.OutputFormat == utils.OutputFormatJSON {
		envelope := extendPendingJSON{
			OK:       false,
			Status:   utils.PendingApprovalStatus,
			ExitCode: utils.ExitCodePendingApproval,
			Message:  message,
			Context: extendPendingCtx{
				Operation:          opExtend,
				WorkSessionID:      id,
				RequestID:          requestID,
				RequestedExpiresAt: requestedExpiresAt,
				RequestExpiresAt:   deadline,
			},
			NextActions: next,
		}
		if err := utils.PrintJSONValue(os.Stdout, envelope); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, `{"ok":false,"status":%q}`+"\n", utils.PendingApprovalStatus)
		}
		return
	}

	utils.CliWarning("%s", message)
	for _, action := range next {
		fmt.Fprintf(os.Stderr, "  %s\n", action.PlainText())
	}
}

// extendPendingJSON is the JSON envelope for extend's 202: the same
// {"status":"pending_approval", ...} shape utils.PrintPendingApproval uses
// elsewhere, but with a Context typed for this surface—requested_expires_at
// and the request's own deadline belong in machine-readable context, not only
// folded into the message text.
type extendPendingJSON struct {
	OK          bool               `json:"ok"`
	Status      string             `json:"status"`
	ExitCode    int                `json:"exit_code"`
	Message     string             `json:"message"`
	Context     extendPendingCtx   `json:"context"`
	NextActions []utils.NextAction `json:"next_actions,omitempty"`
}

type extendPendingCtx struct {
	Operation     string `json:"operation"`
	WorkSessionID string `json:"work_session_id"`
	RequestID     string `json:"request_id,omitempty"`
	// RequestedExpiresAt is the expiry that was asked for; RequestExpiresAt is
	// the request's own deadline—when it lapses undecided, not the session's.
	RequestedExpiresAt string `json:"requested_expires_at,omitempty"`
	RequestExpiresAt   string `json:"request_expires_at,omitempty"`
}

func init() {
	workSessionExtendCmd.Flags().StringVar(&extendExpiresIn, "expires-in", "", "New expiry set to now + duration (e.g. 2h)")
	workSessionExtendCmd.Flags().StringVar(&extendExpiresAt, "expires-at", "", "New absolute expiry time (RFC3339)")
	workSessionExtendCmd.Flags().StringVar(&extendReason, "reason", "", "Why the session needs more time (required)")
	workSessionExtendCmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
}
