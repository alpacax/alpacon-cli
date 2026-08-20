package worksession

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/server"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	pollInterval      = 10 * time.Second
	waitMsgApproval   = "Waiting for approval..."
	waitMsgActivation = "Waiting for activation..."
)

type useDecision int

const (
	useDecisionNoop useDecision = iota
	useDecisionUseNow
	useDecisionErrorNeedsWait
	useDecisionSkipScheduled
)

var validScopePresets = []string{"command", "editor", "sudo", "tunnel", "webftp", "websh"}

var (
	purpose          string
	createScopes     []string
	createServers    []string
	expiresIn        string
	expiresAt        string
	requesterType    string
	wait             bool
	waitApproval     string
	useAfterCreate   bool
	createSudo       []string
	createSudoReason string
)

var workSessionCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new work session",
	Long: `Create a new work session.

Set the session lifetime with --expires-in (relative, e.g. 2h) or --expires-at
(absolute RFC3339); one is required in non-interactive mode.

Pass --use to set the new session as the workspace's active session, so subsequent
exec/websh/cp/tunnel commands attach to it without --work-session. When approval is
required, combine --use with --wait. The session is attached once it reaches the
active state. Use --wait-approval DURATION to wait longer than the default 5 minutes.

If the work needs sudo, pre-declare the command patterns with --sudo. This attaches
MFA-bypass sudo policies to the session so a non-interactive caller (e.g. an AI agent
running 'exec') can run those exact sudo commands without an interactive MFA
prompt. The 'sudo' scope is added automatically, and the policies are submitted for
approval together with the session. If a sudo command is later denied, add it to the
session with 'alpacon work-session update <id> --sudo "<command>"'.

When an AI agent (rather than a human) drives the session, pass --requester-type agent
so it is recorded and scoped accordingly.`,
	Example: `  alpacon work-session create --scope command,websh --server web-01 --expires-in 2h --purpose "restart nginx on web-01 to clear 502s"
  alpacon work-session create --scope command --server web-01,db-01 --expires-at 2027-01-15T10:00:00Z --purpose "deploy" --wait
  alpacon work-session create --scope command --server web-01 --expires-in 1h --purpose "hotfix" --use
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "deploy" --wait --use
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "deploy" --wait-approval 30m --use
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "auto-remediate disk-full alert on web-01: rotate logs, restart rsyslog" --requester-type agent
  alpacon work-session create --server web-01 --expires-in 2h --purpose "nginx hotfix" \
    --sudo "systemctl restart nginx,systemctl reload nginx" --sudo "tail -f /var/log/nginx/*.log"`,
	Run: func(cmd *cobra.Command, args []string) {
		purpose = strings.TrimSpace(purpose)
		if purpose == "" {
			if !utils.IsInteractiveShell() {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "Non-interactive mode requires --purpose.")
			}
			purpose = utils.PromptForRequiredInput("Purpose: ")
		}
		if len(createScopes) == 0 {
			if !utils.IsInteractiveShell() {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "Non-interactive mode requires --scope.")
			}
			createScopes = utils.SplitAndTrim(utils.PromptForRequiredInput("Scopes (comma-separated, e.g. command,websh): "), ",")
		}
		if len(createServers) == 0 {
			if !utils.IsInteractiveShell() {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "Non-interactive mode requires --server.")
			}
			createServers = utils.SplitAndTrim(utils.PromptForRequiredInput("Servers (comma-separated server names): "), ",")
		}

		expiresAtVal, err := parseExpiryFlag(expiresIn, expiresAt)
		if err != nil {
			if expiresIn == "" && expiresAt == "" {
				if !utils.IsInteractiveShell() {
					utils.CliUsageErrorEnvelopeWithExit(opCreate, "Non-interactive mode requires --expires-in or --expires-at.")
				}
				expiresIn = utils.PromptForRequiredInput("Expires in (e.g. 1h, 2h, 4h): ")
				expiresAtVal, err = parseExpiryFlag(expiresIn, "")
				if err != nil {
					utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid expiry: %s.", err)
				}
			} else {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid expiry: %s.", err)
			}
		}

		if requesterType != "user" && requesterType != "agent" {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid --requester-type %q: must be \"user\" or \"agent\".", requesterType)
		}

		waitTimeout, werr := resolveWaitTimeout(wait, waitApproval, cmd.Flags().Changed("wait-approval"))
		if werr != nil {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid wait timeout: %s.", werr)
		}
		shouldWait := waitTimeout > 0

		// Pre-validate --use to avoid creating an orphan server-side session that we
		// can't attach to the workspace.
		if useAfterCreate {
			if requesterType == "agent" {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "--use cannot be combined with --requester-type=agent (agent sessions are not workspace-attachable).")
			}
			cfg, err := config.LoadConfig()
			if err != nil || cfg.WorkspaceName == "" {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "--use requires an active workspace. Run 'alpacon login' first.")
			}
		}

		var scopeList []string
		for _, s := range createScopes {
			if s = strings.TrimSpace(s); s != "" {
				scopeList = append(scopeList, s)
			}
		}
		// Build sudo bypass policies from --sudo. Each --sudo value is a
		// comma-separated list of command patterns forming one policy; the
		// policies are MFA-bypass (the only way a non-interactive caller
		// running 'exec' can sudo). The server binds each policy to
		// the session assignee automatically, so they never apply to other
		// users. The 'sudo' scope is required server-side, so add it implicitly.
		sudoPolicies := buildSudoPolicies(createSudo, createSudoReason)
		if len(sudoPolicies) > 0 && !slices.Contains(scopeList, "sudo") {
			scopeList = append(scopeList, "sudo")
		}

		if len(scopeList) == 0 {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "--scope must contain at least one valid scope.")
		}
		if err := validateScopeEnum(scopeList); err != nil {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid --scope: %s", err)
		}
		if err := validateAgentScopes(requesterType, scopeList); err != nil {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid --scope: %s", err)
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opCreate, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		serverNames := utils.CompactStrings(createServers)
		if len(serverNames) == 0 {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "--server must contain at least one valid server name.")
		}
		serverIDs, err := server.ResolveServerNames(ac, serverNames)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opCreate, err, "%s.", err)
		}

		req := wsapi.WorkSessionCreateRequest{
			Description:   purpose,
			RequesterType: requesterType,
			Scopes:        scopeList,
			Servers:       serverIDs,
			ExpiresAt:     expiresAtVal,
			SudoPolicies:  sudoPolicies,
		}

		session, err := wsapi.CreateWorkSession(ac, req)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opCreate, err, "Failed to create work session: %s.", err)
		}

		if utils.OutputFormat != utils.OutputFormatJSON {
			utils.CliSuccess("%s", createSuccessMessage(session))
		}

		// Phase 1: post-create decision. Cases without an explicit return delegate to
		// the --wait branch below (or exit immediately when --wait is not set).
		switch decideUseAction(session.Status, useAfterCreate) {
		case useDecisionUseNow:
			// Re-fetch so the serialized JSON matches the --wait --use path.
			activeSession, err := RunUseSession(ac, session.ID)
			if err != nil {
				// Partial success (created, use failed)—nil omits the server error_code.
				utils.CliErrorEnvelopeWithExit(opCreate, nil, "Work session created (%s) but failed to set as active: %s. Run 'alpacon work-session use %s' to retry.", session.ID, err, session.ID)
			}
			message := activeWorkSessionSetMessage("", activeSession.ID, activeSession.Description)
			if utils.OutputFormat == utils.OutputFormatJSON {
				active := activeSession.ID
				printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, createSuccessMessage(session)+". "+message, activeSession, &active))
				return
			}
			utils.CliSuccess("%s", message)
			printSessionAdvisories(activeSession)
			return
		case useDecisionSkipScheduled:
			if !shouldWait {
				if utils.OutputFormat == utils.OutputFormatJSON {
					printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, createSuccessMessage(session), session, nil))
					return
				}
				utils.CliInfo("Session is scheduled to activate. Run 'alpacon work-session use %s' once active.", session.ID)
				printSessionAdvisories(session)
				return
			}
		case useDecisionErrorNeedsWait:
			if !shouldWait {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "--use requires the session to be active. Pass --wait (or --wait-approval <duration> for a longer timeout) to wait for approval, or run 'alpacon work-session use %s' after approval.", session.ID)
			}
		}

		if !shouldWait {
			// A session that lands pending needs a human to approve it out of band
			// (ADR 0015). Emit the structured pending-approval signal and exit with
			// ExitCodePendingApproval so a machine consumer (AI agent, CI) can branch
			// on "wait or check later" instead of treating the pending create as a
			// success. Other statuses (e.g. auto-approved) keep the existing success
			// output and exit 0.
			if session.Status == pendingWorkSessionStatus {
				utils.PrintPendingApproval(
					fmt.Sprintf("Approval required—work session %s is pending. A human must approve it in the Alpacon console (web).", session.ID),
					session.ApprovalRequestID,
					utils.NextAction{Command: fmt.Sprintf("alpacon work-session use %s", session.ID), Description: "after approval"},
				)
				os.Exit(utils.ExitCodePendingApproval)
			}
			if utils.OutputFormat == utils.OutputFormatJSON {
				printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, createSuccessMessage(session), session, nil))
				return
			}
			printSessionAdvisories(session)
			return
		}

		// Phase 2: poll. With --use we wait for active; otherwise approved is enough.
		finalSession, err := pollForApproval(ac, session.ID, useAfterCreate, pollInterval, waitTimeout)
		if err != nil {
			// Both branches match 'alpacon event wait': a settled negative outcome is 6,
			// a wait that ran out with the outcome still open is 4. Only a polling
			// failure falls through to the general-error code.
			var terminal *terminalWaitError
			if errors.As(err, &terminal) {
				utils.CliErrorEnvelopeWithExitCode(utils.ExitCodeNotApproved, opCreate, err, "%s", err)
			}
			var pending *pendingWaitError
			if errors.As(err, &pending) {
				utils.PrintPendingApproval(
					fmt.Sprintf("Work session %s: %s. The outcome is still open.", session.ID, err),
					session.ApprovalRequestID,
					utils.NextAction{Command: fmt.Sprintf("alpacon work-session use %s", session.ID), Description: "after approval"},
				)
				os.Exit(utils.ExitCodePendingApproval)
			}
			utils.CliErrorEnvelopeWithExit(opCreate, err, "%s", err)
		}

		if !useAfterCreate {
			message := fmt.Sprintf("Work session %s approved.", session.ID)
			if utils.OutputFormat == utils.OutputFormatJSON {
				printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, message, finalSession, nil))
				return
			}
			utils.CliSuccess("%s", message)
			printSessionAdvisories(finalSession)
			return
		}

		// Phase 3: --wait --use. pollForApproval(untilActive=true) guarantees status reached active.
		desc, err := RunUse(ac, session.ID)
		if err != nil {
			// Partial success (approved, use failed)—nil omits the server error_code.
			utils.CliErrorEnvelopeWithExit(opCreate, nil, "Work session %s approved but failed to set as active: %s. Run 'alpacon work-session use %s' to retry.", session.ID, err, session.ID)
		}
		message := activeWorkSessionSetMessage(fmt.Sprintf("Work session %s approved. ", session.ID), session.ID, desc)
		if utils.OutputFormat == utils.OutputFormatJSON {
			active := session.ID
			printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, message, finalSession, &active))
			return
		}
		utils.CliSuccess("%s", message)
		printSessionAdvisories(finalSession)
	},
}

// terminalWaitError marks a wait that ended in a status the session can never leave.
// Distinguished from a polling failure so the two do not share an exit code: an agent
// that reads only the exit code would otherwise retry a rejected request forever.
type terminalWaitError struct {
	message string
}

func (e *terminalWaitError) Error() string { return e.message }

// pendingWaitError marks a wait that ended with the outcome still open—the window
// elapsed, or the CLI could not reach the server for a bounded run of polls. Either
// way the session exists and a human has not decided yet, so it carries the same exit
// code a create without --wait does, rather than the general-error code that reads as
// retryable and gets answered with a second session.
type pendingWaitError struct {
	message string
}

func (e *pendingWaitError) Error() string { return e.message }

// parseExpiryFlag validates the --expires-in / --expires-at mutual exclusion
// and returns an RFC3339 expires_at string.
func parseExpiryFlag(expiresIn, expiresAt string) (string, error) {
	expiresIn = strings.TrimSpace(expiresIn)
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresIn != "" && expiresAt != "" {
		return "", errors.New("--expires-in and --expires-at are mutually exclusive")
	}
	if expiresIn == "" && expiresAt == "" {
		return "", errors.New("one of --expires-in or --expires-at is required")
	}
	if expiresIn != "" {
		d, err := utils.ParsePositiveDuration("--expires-in", expiresIn)
		if err != nil {
			return "", err
		}
		return time.Now().UTC().Add(d).Format(time.RFC3339), nil
	}
	if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		return "", fmt.Errorf("invalid --expires-at value %q: must be RFC3339 format", expiresAt)
	}
	return expiresAt, nil
}

// resolveWaitTimeout uses waitApprovalSet to separate an unset --wait-approval
// from an explicitly empty value, which must be rejected rather than silently ignored.
func resolveWaitTimeout(waitFlag bool, waitApprovalRaw string, waitApprovalSet bool) (time.Duration, error) {
	waitApprovalRaw = strings.TrimSpace(waitApprovalRaw)
	if waitApprovalRaw == "" && !waitApprovalSet {
		if waitFlag {
			return utils.DefaultApprovalWaitTimeout, nil
		}
		return 0, nil
	}
	return utils.ParsePositiveDuration("--wait-approval", waitApprovalRaw)
}

// validateScopeEnum rejects scopes not in validScopePresets and lists the
// allowed values in the error message. The caller is expected to prefix the
// error with the relevant flag name (e.g. "Invalid --scope: ...").
func validateScopeEnum(scopes []string) error {
	allowed := make(map[string]struct{}, len(validScopePresets))
	for _, s := range validScopePresets {
		allowed[s] = struct{}{}
	}
	var unknown []string
	for _, s := range scopes {
		if _, ok := allowed[s]; !ok {
			unknown = append(unknown, s)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s. valid: %s",
		strings.Join(unknown, ", "),
		strings.Join(validScopePresets, ", "))
}

func decideUseAction(status string, useEnabled bool) useDecision {
	if !useEnabled {
		return useDecisionNoop
	}
	switch status {
	case activeWorkSessionStatus:
		return useDecisionUseNow
	case approvedWorkSessionStatus:
		return useDecisionSkipScheduled
	default:
		return useDecisionErrorNeedsWait
	}
}

// validateAgentScopes returns an error when requester_type is "agent" and
// scopes contains "websh", which the server disallows.
func validateAgentScopes(requesterType string, scopes []string) error {
	if requesterType != "agent" {
		return nil
	}
	if slices.Contains(scopes, "websh") {
		return errors.New("\"websh\" is not allowed for agent sessions")
	}
	return nil
}

// buildSudoPolicies turns repeatable --sudo values into MFA-bypass policy
// definitions. Each value is a comma-separated list of command patterns
// (wildcards permitted) forming one policy. Empty values are skipped.
func buildSudoPolicies(specs []string, reason string) []wsapi.SudoPolicyInline {
	var policies []wsapi.SudoPolicyInline
	for _, spec := range specs {
		commands := utils.SplitAndTrim(spec, ",")
		if len(commands) == 0 {
			continue
		}
		policies = append(policies, wsapi.SudoPolicyInline{
			Commands:       commands,
			Reason:         reason,
			AllowBypassMFA: true,
		})
	}
	return policies
}

// pollForApproval polls at interval until the session reaches a terminal state or
// timeout elapses. untilActive=false returns on approved or active; untilActive=true
// returns only on active (continues polling on approved until the server
// auto-activates). Deadline-based rather than attempt-count-based so a timeout
// under one interval (e.g. --wait-approval 15s) still waits the full duration.
// A failed poll does not end the wait—one 429 would otherwise discard a
// half-hour wait—unless it will repeat (a fatal 4xx) or already has.
func pollForApproval(ac *client.AlpaconClient, id string, untilActive bool, interval, timeout time.Duration) (*wsapi.WorkSession, error) {
	deadline := time.Now().Add(timeout)
	timedOut := &pendingWaitError{message: fmt.Sprintf("timed out waiting for approval after %s", timeout)}
	failures := 0
	for {
		s, err := wsapi.GetWorkSession(ac, id)
		if err != nil {
			failures++
			if !utils.IsTransientRequestError(err) {
				return nil, fmt.Errorf("polling failed: %w", err)
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil, timedOut
			}
			if failures >= utils.MaxConsecutivePollFailures {
				// The session is created and pending, so this exits on the pending
				// contract like the timeout does. Exit 1 reads as retryable, and an
				// agent answers it by re-running create and filing a second session
				// and a second approval request.
				utils.CliWarning("Approval wait gave up after %d failed polls (%s); the work session is still pending.", failures, err)
				return nil, &pendingWaitError{message: fmt.Sprintf("gave up after %d failed polls", failures)}
			}
			utils.CliWarning("Poll failed (%s); still waiting.", err)
			pollSleep(remaining, utils.NextPollBackoff(interval, failures-1, utils.RetryAfter(err)))
			continue
		}
		failures = 0
		switch s.Status {
		case activeWorkSessionStatus:
			return s, nil
		case approvedWorkSessionStatus:
			if !untilActive {
				return s, nil
			}
		case rejectedWorkSessionStatus:
			return nil, &terminalWaitError{message: "work session was rejected"}
		case expiredWorkSessionStatus:
			return nil, &terminalWaitError{message: "work session expired while waiting for approval"}
		case revokedWorkSessionStatus:
			return nil, &terminalWaitError{message: "work session was revoked"}
		case cancelledWorkSessionStatus:
			return nil, &terminalWaitError{message: "work session was cancelled"}
		case completedWorkSessionStatus:
			return nil, &terminalWaitError{message: "work session was completed unexpectedly"}
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, timedOut
		}
		waitMsg := waitMsgApproval
		if s.Status == approvedWorkSessionStatus {
			waitMsg = waitMsgActivation
		}
		utils.CliInfo("%s (%s elapsed of %s)", waitMsg, (timeout - remaining).Round(time.Second), timeout)
		pollSleep(remaining, interval)
	}
}

// pollSleep waits want, capped at remaining (must be positive); a non-positive
// want falls back to remaining rather than spinning.
func pollSleep(remaining, want time.Duration) {
	step := min(remaining, want)
	if step <= 0 {
		step = remaining
	}
	time.Sleep(step)
}

func init() {
	workSessionCreateCmd.Flags().StringVar(&purpose, "purpose", "", "What you're doing and why; be specific. Markdown supported. (required in non-interactive mode)")
	workSessionCreateCmd.Flags().StringSliceVar(&createScopes, "scope", nil, "Scopes to request. Valid: command, editor, sudo, tunnel, webftp, websh (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringSliceVar(&createServers, "server", nil, "Target server names (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringVar(&expiresIn, "expires-in", "", "Session duration (e.g. 1h, 2h, 4h)")
	workSessionCreateCmd.Flags().StringVar(&expiresAt, "expires-at", "", "Absolute expiry time (RFC3339)")
	workSessionCreateCmd.Flags().StringVar(&requesterType, "requester-type", "user", "Requester type: 'user' (default) or 'agent' (set when an AI agent drives the session)")
	workSessionCreateCmd.Flags().BoolVar(&wait, "wait", false, "Poll until the session is approved, then exit (default timeout 5m; does not set as active; combine with --use to attach automatically)")
	workSessionCreateCmd.Flags().StringVar(&waitApproval, "wait-approval", "", "Like --wait with a custom wait timeout (e.g. 30m; default 5m). Implies --wait")
	workSessionCreateCmd.Flags().BoolVar(&useAfterCreate, "use", false, "Set the created session as the workspace's active session (requires status to reach 'active'; combine with --wait when approval is needed)")
	workSessionCreateCmd.Flags().StringArrayVar(&createSudo, "sudo", nil, "Pre-declare sudo command patterns to run without interactive MFA (repeatable; each value is a comma-separated pattern list forming one policy, wildcards allowed; literal commas inside a pattern are not supported — pass the flag again for each policy that needs them). Required for non-interactive sudo via 'exec' (e.g. AI agents). Implies the 'sudo' scope. Patterns are submitted for approval with the session.")
	workSessionCreateCmd.Flags().StringVar(&createSudoReason, "sudo-reason", "", "Justification applied to the sudo policies created via --sudo")
	workSessionCreateCmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
}
