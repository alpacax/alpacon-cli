package worksession

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	extendExpiresIn    string
	extendExpiresAt    string
	extendReason       string
	extendWait         bool
	extendWaitApproval string
)

var workSessionExtendCmd = &cobra.Command{
	Use:   "extend SESSION_ID",
	Short: "Extend the expiry of an approved or active work session",
	Long: `Extend the expiry of an approved or active work session.

Some workspaces gate extension behind approval. When that is on, --reason is
required—a short justification an approver judges the request by—and the
session may not extend immediately: the server answers with the extension
request pending instead. Without --wait the CLI reports it and exits (see
"Exit codes" in the README); with --wait it polls until the request is
decided (default timeout 5m; raise it with --wait-approval).`,
	Args: cobra.ExactArgs(1),
	Example: `  alpacon work-session extend ses-abc123 --expires-in 2h
  alpacon work-session extend ses-abc123 --expires-at 2026-05-09T10:00:00Z
  alpacon work-session extend ses-abc123 --expires-in 2h --reason "customer escalation, still triaging"
  alpacon work-session extend ses-abc123 --expires-in 2h --reason "customer escalation" --wait`,
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

		waitTimeout, werr := resolveWaitTimeout(extendWait, extendWaitApproval, cmd.Flags().Changed("wait-approval"))
		if werr != nil {
			utils.CliUsageErrorEnvelopeWithExit(opExtend, "Invalid wait timeout: %s.", werr)
		}
		shouldWait := waitTimeout > 0

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opExtend, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		req := wsapi.WorkSessionExtendRequest{ExpiresAt: expiresAtVal, Reason: strings.TrimSpace(extendReason)}
		session, err := wsapi.ExtendWorkSession(ac, id, req)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opExtend, err, "%s", extendErrorMessage(id, err))
		}

		if session.PendingExtensionRequest == nil {
			printExtendSuccess(id, formatMutationExpiresAt(session.ExpiresAt))
			return
		}

		pending := session.PendingExtensionRequest
		if !shouldWait {
			utils.PrintPendingApproval(
				extendPendingMessage(id, pending),
				pending.ID,
				utils.NextAction{Command: fmt.Sprintf("alpacon work-session extend %s --wait", id), Description: "block until decided"},
			)
			os.Exit(utils.ExitCodePendingApproval)
		}

		finalSession, werr := pollForExtensionApproval(ac, id, pending.ID, pollInterval, waitTimeout)
		if werr != nil {
			var terminal *terminalWaitError
			if errors.As(werr, &terminal) {
				if utils.OutputFormat == utils.OutputFormatJSON {
					printTerminalWaitErrorJSON(opExtend, terminal, werr)
					os.Exit(utils.ExitCodeNotApproved)
				}
				printTerminalWaitError(terminal, werr)
				os.Exit(utils.ExitCodeNotApproved)
			}
			var waitPending *pendingWaitError
			if errors.As(werr, &waitPending) {
				utils.PrintPendingApproval(
					fmt.Sprintf("Work session %s: %s. The outcome is still open.", id, werr),
					pending.ID,
					utils.NextAction{Command: fmt.Sprintf("alpacon work-session extend %s --wait", id), Description: "keep waiting"},
				)
				os.Exit(utils.ExitCodePendingApproval)
			}
			utils.CliErrorEnvelopeWithExit(opExtend, werr, "%s", werr)
		}

		printExtendSuccess(id, formatMutationExpiresAt(finalSession.ExpiresAt))
	},
}

// printExtendSuccess writes the extended-session result: the mutation JSON
// envelope under --output json, a plain success line otherwise. Shared by the
// immediate-200 path and the --wait path that reaches approval after polling.
func printExtendSuccess(id, expiresAt string) {
	output := newWorkSessionExtendOutput(id, expiresAt)
	if utils.OutputFormat == utils.OutputFormatJSON {
		printWorkSessionMutationJSON(output)
		return
	}
	utils.CliSuccess("%s", output.Message)
}

// pollForExtensionApproval polls the pending extension request until it
// leaves 'pending' or timeout elapses. On approval it re-reads the session so
// the caller gets the expires_at an approver may have narrowed
// (adjusted_expires_at), never the raw value originally requested. Pacing and
// failure handling mirror pollForApproval's wait for session creation—same
// primitives, a different resource (the approval request, not the session,
// is what changes state here).
func pollForExtensionApproval(ac *client.AlpaconClient, sessionID, requestID string, interval, timeout time.Duration) (*wsapi.WorkSession, error) {
	start := time.Now()
	deadline := start.Add(timeout)
	timedOut := &pendingWaitError{message: fmt.Sprintf("timed out waiting for extension approval after %s", timeout)}
	failures := 0
	throttles := 0
	budget := utils.NewThrottleBudget(timeout)
	lastStatus := ""
	for {
		reqStatus, err := approvalapi.GetApprovalRequest(ac, requestID)
		if err != nil {
			if !utils.IsTransientRequestError(err) {
				return nil, fmt.Errorf("polling failed: %w", err)
			}
			if utils.HTTPStatusCode(err) == http.StatusTooManyRequests {
				delay := utils.NextPollBackoff(interval, throttles, utils.RetryAfter(err))
				throttles++
				budget.WarnThrottled(delay)
				if newDeadline, extended := budget.Extend(deadline, delay); extended {
					deadline = newDeadline
				}
				if !pollSleep(deadline, delay) {
					return nil, timedOut
				}
				continue
			}
			failures++
			if !time.Now().Before(deadline) {
				return nil, timedOut
			}
			if failures >= utils.MaxConsecutivePollFailures {
				utils.CliWarning("Approval wait gave up after %d failed polls (%s); the extension request is still pending.", failures, err)
				return nil, &pendingWaitError{message: fmt.Sprintf("gave up after %d failed polls", failures)}
			}
			utils.CliWarning("Poll failed (%s); still waiting.", err)
			if !pollSleep(deadline, utils.NextPollBackoff(interval, failures-1, utils.RetryAfter(err))) {
				return nil, timedOut
			}
			continue
		}
		failures = 0
		throttles = 0
		if lastStatus != "" && reqStatus.Status != lastStatus {
			budget.Reset()
		}
		lastStatus = reqStatus.Status
		switch reqStatus.Status {
		case "approved":
			session, err := wsapi.GetWorkSession(ac, sessionID)
			if err != nil {
				return nil, fmt.Errorf("extension approved but failed to re-read work session %s: %w", sessionID, err)
			}
			return session, nil
		case "rejected":
			return nil, &terminalWaitError{message: "extension request was rejected", sessionID: sessionID}
		case "expired":
			return nil, &terminalWaitError{message: "extension request expired before it was decided", sessionID: sessionID}
		case "cancelled":
			return nil, &terminalWaitError{message: "extension request was cancelled", sessionID: sessionID}
		}

		elapsed := time.Since(start)
		window := elapsed + time.Until(deadline)
		utils.CliInfo("Waiting for extension approval... (%s elapsed of %s)", elapsed.Round(time.Second), window.Round(time.Second))
		if !pollSleep(deadline, utils.NextPollTick(interval, elapsed)) {
			return nil, timedOut
		}
	}
}

func init() {
	workSessionExtendCmd.Flags().StringVar(&extendExpiresIn, "expires-in", "", "New expiry set to now + duration (e.g. 2h)")
	workSessionExtendCmd.Flags().StringVar(&extendExpiresAt, "expires-at", "", "New absolute expiry time (RFC3339)")
	workSessionExtendCmd.Flags().StringVar(&extendReason, "reason", "", "Why the session needs more time; required when the workspace gates extension behind approval, ignored otherwise")
	workSessionExtendCmd.Flags().BoolVar(&extendWait, "wait", false, "Poll until a pending extension request is decided, then exit (default timeout 5m; no-op when the extension applied immediately)")
	workSessionExtendCmd.Flags().StringVar(&extendWaitApproval, "wait-approval", "", "Like --wait with a custom wait timeout (e.g. 30m; default 5m). Implies --wait")
	workSessionExtendCmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
}
