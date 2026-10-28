package worksession

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/server"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	pollInterval      = 10 * time.Second
	waitMsgApproval   = "Waiting for approval..."
	waitMsgActivation = "Waiting for activation..."
)

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
	createAccount    string
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
session-authorized sudo policies so a non-interactive caller (e.g. an AI agent
running 'exec') can run matching sudo commands inside the approved work session
without an MFA prompt. The 'sudo' scope is added automatically, and the policies are submitted for
approval together with the session. If a sudo command is later denied, add it to the
session with 'alpacon work-session update <id> --sudo "<command>"'.

When an AI agent drives the CLI, the session is an agent session. The CLI detects
Claude Code, Codex, Gemini CLI, GitHub Copilot, Cursor, Cline, and Goose from the
environment variables they set in the shells they run, and names the agent on every
request. For an agent the CLI does not detect (for example Aider), set
AI_AGENT=<name> in the agent's environment. --requester-type agent opens an agent
session without naming an agent; --requester-type user is refused while an agent is
detected.

An agent session refuses --use and the websh scope, so pass --work-session SESSION_ID
before the server on each command instead, as in 'alpacon exec --work-session
SESSION_ID web-01 -- uptime'. --wait still waits for approval.

Pass --account to name the account the work runs as on the servers (for example
root), so the approver sees it with the request. The session runs only as that
account, so pass it to later commands with -u <account> (for example 'alpacon exec
-u root web-01 -- uptime').`,
	Example: `  alpacon work-session create --scope command,websh --server web-01 --expires-in 2h --purpose "restart nginx on web-01 to clear 502s"
  alpacon work-session create --scope command --server web-01,db-01 --expires-at 2027-01-15T10:00:00Z --purpose "deploy" --wait
  alpacon work-session create --scope command --server web-01 --expires-in 1h --purpose "hotfix" --use
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "deploy" --wait --use
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "deploy" --wait-approval 30m --use

  # AI agent: no --use and no websh scope; name the session on each command instead
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "auto-remediate disk-full alert on web-01: rotate logs, restart rsyslog" --requester-type agent --wait
  alpacon exec --work-session SESSION_ID web-01 -- df -h
  alpacon work-session create --scope command,sudo --server web-01 --expires-in 1h --purpose "restart nginx" --account root

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
		detected := aiagent.FromEnvironment()
		sessionRequesterType, err := resolveRequesterType(requesterType, cmd.Flags().Changed("requester-type"), detected)
		if err != nil {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "%s", err)
		}
		account, err := normalizeAccount(createAccount, cmd.Flags().Changed("account"))
		if err != nil {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "%s", err)
		}

		waitTimeout, werr := resolveWaitTimeout(wait, waitApproval, cmd.Flags().Changed("wait-approval"))
		if werr != nil {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid wait timeout: %s.", werr)
		}
		shouldWait := waitTimeout > 0

		// Pre-validate --use to avoid creating an orphan server-side session that we
		// can't attach to the workspace.
		if useAfterCreate {
			if sessionRequesterType == "agent" {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "--use cannot be combined with an agent session (agent sessions are not workspace-attachable; pass the session ID to each command with --work-session).%s", detectedAgentSuffix(detected))
			}
			cfg, err := config.LoadConfig()
			if err != nil || cfg.WorkspaceName == "" {
				utils.CliErrorEnvelopeWithExit(opCreate, utils.ErrNotLoggedIn, "--use requires an active workspace. Run 'alpacon login' first.")
			}
		}

		var scopeList []string
		for _, s := range createScopes {
			if s = strings.TrimSpace(s); s != "" {
				scopeList = append(scopeList, s)
			}
		}
		// Build session-authorized sudo policies from --sudo. Each --sudo value is a
		// comma-separated list of command patterns forming one policy; the
		// policies are session-authorized (the only way a non-interactive caller
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
		if err := validateAgentScopes(sessionRequesterType, scopeList); err != nil {
			utils.CliUsageErrorEnvelopeWithExit(opCreate, "Invalid --scope: %s.%s", err, detectedAgentSuffix(detected))
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
			Description:           purpose,
			RequesterType:         sessionRequesterType,
			Scopes:                scopeList,
			Servers:               serverIDs,
			ExpiresAt:             expiresAtVal,
			SudoPolicies:          sudoPolicies,
			TargetAccountUsername: account,
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
				if terminal := settledNegatively(session.Status, session.ID); terminal != nil {
					exitTerminalWait(opCreate, terminal, terminal)
				}
				if session.Status != wsapi.StatusPending {
					utils.CliErrorEnvelopeWithExit(opCreate, nil, "--use requires the session to be active, but work session %s is %s.", session.ID, session.Status)
				}
				utils.PrintPendingApproval(
					fmt.Sprintf("--use requires the session to be active, and work session %s is pending. A human must approve it in the Alpacon console (web). Pass --wait (or --wait-approval <duration> for a longer timeout) to wait for approval, or run 'alpacon work-session use %s' after approval.", session.ID, session.ID),
					session.ApprovalRequestID,
					afterApprovalAction(sessionRequesterType, session.ID, account),
				)
				os.Exit(utils.ExitCodePendingApproval)
			}
		}

		if !shouldWait {
			// A session that lands pending needs a human to approve it out of
			// band. Emit the structured pending-approval signal and exit with
			// ExitCodePendingApproval so a machine consumer (AI agent, CI) can branch
			// on "wait or check later" instead of treating the pending create as a
			// success. Other statuses (e.g. auto-approved) keep the existing success
			// output and exit 0.
			if session.Status == wsapi.StatusPending {
				utils.PrintPendingApproval(
					fmt.Sprintf("Approval required—work session %s is pending. A human must approve it in the Alpacon console (web).", session.ID),
					session.ApprovalRequestID,
					afterApprovalAction(sessionRequesterType, session.ID, account),
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
				exitTerminalWait(opCreate, terminal, err)
			}
			var pending *pendingWaitError
			if errors.As(err, &pending) {
				// The refusal a timed-out wait ended on, if any, adds its hint.
				utils.PrintPendingApproval(
					fmt.Sprintf("Work session %s: %s. The outcome is still open.", session.ID, err),
					session.ApprovalRequestID,
					afterApprovalAction(sessionRequesterType, session.ID, account),
					utils.RefusalNextActions(err)...,
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

// useDecision is what the post-create switch does about --use: nothing, attach
// now, refuse a session no approval can attach yet unless --wait was passed, or
// wait out one the server has approved but not activated.
type useDecision int

// terminalWaitError marks a wait that ended in a status the session can never leave.
// Distinguished from a polling failure so the two do not share an exit code: an agent
// that reads only the exit code would otherwise retry a rejected request forever.
type terminalWaitError struct {
	message string
	// sessionID names the session the wait left behind. The session outlives a
	// refusal, so a caller told only "rejected" has no way back to it.
	sessionID string
}

func (e *terminalWaitError) Error() string {
	if e.sessionID == "" {
		return e.message
	}
	return fmt.Sprintf("%s (work session %s)", e.message, e.sessionID)
}

// pendingWaitError marks a wait that ended with the outcome still open—the window
// elapsed, or the CLI could not reach the server for a bounded run of polls. Either
// way the session exists and a human has not decided yet, so it carries the same exit
// code a create without --wait does, rather than the general-error code that reads as
// retryable and gets answered with a second session.
type pendingWaitError struct {
	message string
	// hint is the server's hint of the refusal the last poll got when the wait
	// ended on it, so it still prints. Only the hint is kept: the refusal's code
	// must not reclassify a wait that is still pending.
	hint string
}

func (e *pendingWaitError) Error() string     { return e.message }
func (e *pendingWaitError) ErrorHint() string { return e.hint }

// terminalWaitErrorCtx is the JSON error envelope context for a wait that
// settled without a grant. WorkSessionID is a structured field here—not only
// folded into the message text—so a machine consumer can act on it without
// parsing prose.
type terminalWaitErrorCtx struct {
	Operation     string `json:"operation"`
	WorkSessionID string `json:"work_session_id,omitempty"`
}

// printTerminalWaitErrorJSON writes the settled-without-a-grant refusal as a
// structured envelope, with the follow-ups as NextActions rather than folded
// into the message text.
func printTerminalWaitErrorJSON(operation string, terminal *terminalWaitError, err error) {
	utils.PrintJSONError(os.Stderr, utils.JSONErrorEnvelope[terminalWaitErrorCtx]{
		OK:          false,
		ExitCode:    utils.ExitCodeNotApproved,
		ErrorCode:   utils.ErrorCodeNotApproved,
		Message:     err.Error(),
		Context:     terminalWaitErrorCtx{Operation: operation, WorkSessionID: terminal.sessionID},
		NextActions: terminalWaitNextActions(terminal.sessionID),
	})
}

// terminalWaitNextActions is what a caller can still do with the session the
// refusal left behind: read why it settled, or give it up. Not `work-session
// use`—that is the pending path's follow-up, and RunUseSession refuses anything
// but an active session, which none of these statuses can become again.
func terminalWaitNextActions(sessionID string) []utils.NextAction {
	return []utils.NextAction{
		{Command: fmt.Sprintf("alpacon work-session describe %s", sessionID), Description: "why it settled"},
		{Command: fmt.Sprintf("alpacon work-session complete %s", sessionID)},
	}
}

// printTerminalWaitError writes the refusal and its follow-ups to stderr in the
// shape every other next-action surface uses. Assembled and written once rather
// than through CliError, which appends the report-a-bug pointer: a reviewer
// saying no is the feature working, so the gate refusal (exit 3) leaves that
// pointer off too.
func printTerminalWaitError(terminal *terminalWaitError, err error) {
	var sb strings.Builder
	// The message carries a server-issued session id, so it takes the sanitizer
	// CliError would otherwise have applied on the way out.
	message, _ := utils.SanitizeTerminalBlock(err.Error())
	fmt.Fprintf(&sb, "%s: %s\n", utils.Red("Error"), message)
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "Next:")
	for _, action := range terminalWaitNextActions(terminal.sessionID) {
		fmt.Fprintf(&sb, "  %s\n", action.PlainText())
	}
	fmt.Fprint(os.Stderr, sb.String())
}

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
	case wsapi.StatusActive:
		return useDecisionUseNow
	case wsapi.StatusApproved:
		return useDecisionSkipScheduled
	default:
		return useDecisionErrorNeedsWait
	}
}

// resolveRequesterType picks the session's requester_type. --requester-type
// sets the mode only; the agent's name always comes from the environment. A
// detected agent gets an agent session, and an explicit --requester-type user
// while one is detected is refused: the server holds a declared agent to an
// agent session anyway, and a person taken for an agent needs to learn why
// here rather than from a refused session later.
func resolveRequesterType(flagValue string, flagSet bool, detected aiagent.Detection) (string, error) {
	if !detected.Detected() {
		return flagValue, nil
	}
	if flagSet && flagValue == "user" {
		return "", fmt.Errorf("--requester-type user is refused: this shell runs under the AI agent %s. Unset those variables, or run the command from your own terminal", detected.Describe())
	}
	return "agent", nil
}

// afterApprovalAction is the follow-up for a session still waiting on approval.
// A user session is attached with 'work-session use'; an agent session cannot
// be, so its ID goes to each subsequent command with --work-session instead.
func afterApprovalAction(requesterType, id, account string) utils.NextAction {
	asAccount := ""
	if account != "" {
		asAccount = fmt.Sprintf("; run exec with -u %s, the account the session was created for", account)
	}
	if requesterType == "agent" {
		return utils.NextAction{Description: fmt.Sprintf("after approval, pass --work-session %s to each subsequent command (exec, cp, tunnel)%s", id, asAccount)}
	}
	return utils.NextAction{Command: fmt.Sprintf("alpacon work-session use %s", id), Description: "after approval" + asAccount}
}

// detectedAgentSuffix explains, after a refusal that applies to agent sessions,
// that the session is one because an agent was detected. It is empty when no
// agent was detected.
func detectedAgentSuffix(detected aiagent.Detection) string {
	if !detected.Detected() {
		return ""
	}
	return fmt.Sprintf(" This shell runs under the AI agent %s; not an AI agent? Unset those variables, or run the command from your own terminal.", detected.Describe())
}

// normalizeAccount trims --account and refuses a value that names nothing,
// which the server would otherwise read as no declaration at all.
func normalizeAccount(raw string, set bool) (string, error) {
	account := strings.TrimSpace(raw)
	if set && account == "" {
		return "", errors.New("--account must name an account, such as root")
	}
	return account, nil
}

// validateAgentScopes returns an error when requester_type is "agent" and
// scopes contains one an agent session cannot hold (websh), which the server
// disallows.
func validateAgentScopes(requesterType string, scopes []string) error {
	if requesterType != "agent" {
		return nil
	}
	for _, scope := range scopes {
		if utils.IsAgentForbiddenScope(scope) {
			return fmt.Errorf("%q is not allowed for agent sessions", scope)
		}
	}
	return nil
}

// buildSudoPolicies turns repeatable --sudo values into session-authorized sudo policy
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
			Commands:          commands,
			Reason:            reason,
			SessionAuthorized: true,
		})
	}
	return policies
}

// settledNegatively is the refusal a status stands for once the session can no longer
// become approved or active; nil for any other status.
func settledNegatively(status, id string) *terminalWaitError {
	switch status {
	case wsapi.StatusRejected:
		return &terminalWaitError{message: "work session was rejected", sessionID: id}
	case wsapi.StatusExpired:
		return &terminalWaitError{message: "work session expired while waiting for approval", sessionID: id}
	case wsapi.StatusRevoked:
		return &terminalWaitError{message: "work session was revoked", sessionID: id}
	case wsapi.StatusCancelled:
		return &terminalWaitError{message: "work session was cancelled", sessionID: id}
	case wsapi.StatusCompleted:
		return &terminalWaitError{message: "work session was completed unexpectedly", sessionID: id}
	}
	return nil
}

// exitTerminalWait reports a settled negative outcome and exits 6, in the shape --output selects.
func exitTerminalWait(operation string, terminal *terminalWaitError, err error) {
	if utils.OutputFormat == utils.OutputFormatJSON {
		printTerminalWaitErrorJSON(operation, terminal, err)
	} else {
		printTerminalWaitError(terminal, err)
	}
	os.Exit(utils.ExitCodeNotApproved)
}

// pollForApproval polls at interval until the session reaches a terminal state or
// timeout elapses. untilActive=false returns on approved or active; untilActive=true
// returns only on active (continues polling on approved until the server
// auto-activates). Deadline-based rather than attempt-count-based so a timeout
// under one interval (e.g. --wait-approval 15s) still waits the full duration.
// A failed poll does not end the wait—one 429 would otherwise discard a
// half-hour wait. A fatal 4xx ends it at once and a run of other transient
// failures ends it at MaxConsecutivePollFailures, but a run of 429s does not:
// the throttle budget and the deadline bound that instead.
func pollForApproval(ac *client.AlpaconClient, id string, untilActive bool, interval, timeout time.Duration) (*wsapi.WorkSession, error) {
	start := time.Now()
	deadline := start.Add(timeout)
	// lastRefusal is the refusal (a 429 in practice) the latest poll got, if it
	// got one: a wait that times out on it keeps it behind the timeout, so its
	// hint still reaches the output.
	var lastRefusal error
	timedOut := func() error {
		return &pendingWaitError{message: fmt.Sprintf("timed out waiting for approval after %s", timeout), hint: utils.RefusalHint(lastRefusal)}
	}
	failures := 0
	throttles := 0
	budget := utils.NewThrottleBudget(timeout)
	lastStatus := ""
	for {
		s, err := wsapi.GetWorkSession(ac, id)
		lastRefusal = nil
		if err != nil && utils.IsRefusalStatus(utils.HTTPStatusCode(err)) {
			lastRefusal = err
		}
		if err != nil {
			if !utils.IsTransientRequestError(err) {
				return nil, fmt.Errorf("polling failed: %w", err)
			}
			// A 429 says the server is answering, so it is not counted toward the
			// cap below—the throttle budget and the deadline bound it instead, and
			// counting it would end the wait before the budget it just spent could
			// carry it anywhere. Uncapped, it also warns once rather than per poll.
			if utils.HTTPStatusCode(err) == http.StatusTooManyRequests {
				delay := utils.NextPollBackoff(interval, throttles, utils.RetryAfter(err))
				throttles++
				budget.WarnThrottled(delay)
				if newDeadline, extended := budget.Extend(deadline, delay); extended {
					deadline = newDeadline
				}
				if !pollSleep(deadline, delay) {
					return nil, timedOut()
				}
				continue
			}
			failures++
			// The deadline outranks the cap: once the window is over, "timed out"
			// is the honest answer even when this poll also filled the cap.
			if !time.Now().Before(deadline) {
				return nil, timedOut()
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
			if !pollSleep(deadline, utils.NextPollBackoff(interval, failures-1, utils.RetryAfter(err))) {
				return nil, timedOut()
			}
			continue
		}
		failures = 0
		throttles = 0
		// Only a status change is progress. Resetting on every successful poll would
		// re-earn the whole extension allowance from any non-429 response, leaving
		// the wait no ceiling but the extension count. The first status read is not
		// a change: nothing was observed before it, and the throttles that preceded
		// it are exactly what the allowance is meant to bound.
		if lastStatus != "" && s.Status != lastStatus {
			budget.Reset()
		}
		lastStatus = s.Status
		switch s.Status {
		case wsapi.StatusActive:
			return s, nil
		case wsapi.StatusApproved:
			if !untilActive {
				return s, nil
			}
		}
		if terminal := settledNegatively(s.Status, id); terminal != nil {
			return nil, terminal
		}

		waitMsg := waitMsgApproval
		if s.Status == wsapi.StatusApproved {
			waitMsg = waitMsgActivation
		}
		// Measured from the start rather than from what is left before the
		// deadline, which a 429's extension moves: subtracting a moving deadline
		// would make the reported elapsed time shrink as the wait grew. The window
		// it is reported against moves with the deadline for the same reason—held
		// at the flag's timeout, an extended wait would read as one that outran it.
		elapsed := time.Since(start)
		window := elapsed + time.Until(deadline)
		utils.CliInfo("%s (%s elapsed of %s)", waitMsg, elapsed.Round(time.Second), window.Round(time.Second))
		if !pollSleep(deadline, utils.NextPollTick(interval, elapsed)) {
			return nil, timedOut()
		}
	}
}

// pollSleep waits want, capped at the time left before deadline, and reports
// whether the wait may go on; false means the window closed. The deadline test
// lives here so the wait states it once rather than at every sleep. A
// non-positive want falls back to the whole remainder rather than spinning.
func pollSleep(deadline time.Time, want time.Duration) bool {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false
	}
	step := min(remaining, want)
	if step <= 0 {
		step = remaining
	}
	time.Sleep(step)
	return true
}

func init() {
	workSessionCreateCmd.Flags().StringVar(&purpose, "purpose", "", "What you're doing and why; be specific. Markdown supported. (required in non-interactive mode)")
	workSessionCreateCmd.Flags().StringSliceVar(&createScopes, "scope", nil, "Scopes to request. Valid: command, editor, sudo, tunnel, webftp, websh (repeatable; comma-separated values also accepted; websh is refused with --requester-type agent)")
	workSessionCreateCmd.Flags().StringSliceVar(&createServers, "server", nil, "Target server names (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringVar(&expiresIn, "expires-in", "", "Session duration (e.g. 1h, 2h, 4h)")
	workSessionCreateCmd.Flags().StringVar(&expiresAt, "expires-at", "", "Absolute expiry time (RFC3339)")
	workSessionCreateCmd.Flags().StringVar(&requesterType, "requester-type", "user", "Requester type: 'user' (default) or 'agent'. A detected AI agent always gets 'agent'; set AI_AGENT=<name> for an agent the CLI does not detect")
	workSessionCreateCmd.Flags().BoolVar(&wait, "wait", false, "Poll until the session is approved, then exit (default timeout 5m; does not set as active; combine with --use to attach automatically)")
	workSessionCreateCmd.Flags().StringVar(&waitApproval, "wait-approval", "", "Like --wait with a custom wait timeout (e.g. 30m; default 5m). Implies --wait")
	workSessionCreateCmd.Flags().BoolVar(&useAfterCreate, "use", false, "Set the created session as the workspace's active session (requires status to reach 'active'; combine with --wait when approval is needed; refused with --requester-type agent)")
	workSessionCreateCmd.Flags().StringArrayVar(&createSudo, "sudo", nil, "Pre-declare sudo command patterns to run without interactive MFA (repeatable; each value is a comma-separated pattern list forming one policy, wildcards allowed; literal commas inside a pattern are not supported — pass the flag again for each policy that needs them). Required for non-interactive sudo via 'exec' (e.g. AI agents). Implies the 'sudo' scope. Patterns are submitted for approval with the session.")
	workSessionCreateCmd.Flags().StringVar(&createSudoReason, "sudo-reason", "", "Justification applied to the sudo policies created via --sudo")
	workSessionCreateCmd.Flags().StringVar(&createAccount, "account", "", "Account the work runs as on the servers (e.g. root); the approver sees it with the request. Pass the same account to later commands with -u <account> (e.g. exec -u root)")
	workSessionCreateCmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
}
