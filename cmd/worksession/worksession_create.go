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
	pollMaxAttempts   = 30
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
	waitApproval     bool
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
active state.

If the work needs sudo, pre-declare the command patterns with --sudo. This attaches
MFA-bypass sudo policies to the session so a non-interactive caller (e.g. an AI agent
running 'exec') can run those exact sudo commands without an interactive MFA
prompt. The 'sudo' scope is added automatically, and the policies are submitted for
approval together with the session. If a sudo command is later denied, add it to the
session with 'alpacon work-session update <id> --sudo "<command>"'.

When an AI agent (rather than a human) drives the session, pass --requester-type agent
so it is recorded and scoped accordingly.`,
	Example: `  alpacon work-session create --scope command,websh --server web-01 --expires-in 2h --purpose "nginx fix"
  alpacon work-session create --scope command --server web-01,db-01 --expires-at 2027-01-15T10:00:00Z --purpose "deploy" --wait
  alpacon work-session create --scope command --server web-01 --expires-in 1h --purpose "hotfix" --use
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "deploy" --wait --use
  alpacon work-session create --scope command --server web-01 --expires-in 2h --purpose "automated remediation" --requester-type agent
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
			createScopes = splitCSV(utils.PromptForRequiredInput("Scopes (comma-separated, e.g. command,websh): "))
		}
		if len(createServers) == 0 {
			if !utils.IsInteractiveShell() {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "Non-interactive mode requires --server.")
			}
			createServers = splitCSV(utils.PromptForRequiredInput("Servers (comma-separated server names): "))
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
			return
		case useDecisionSkipScheduled:
			if !waitApproval {
				if utils.OutputFormat == utils.OutputFormatJSON {
					printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, createSuccessMessage(session), session, nil))
					return
				}
				utils.CliInfo("Session is scheduled to activate. Run 'alpacon work-session use %s' once active.", session.ID)
				return
			}
		case useDecisionErrorNeedsWait:
			if !waitApproval {
				utils.CliUsageErrorEnvelopeWithExit(opCreate, "--use requires the session to be active. Pass --wait to wait for approval, or run 'alpacon work-session use %s' after approval.", session.ID)
			}
		}

		if !waitApproval {
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
					fmt.Sprintf("alpacon work-session use %s  # after approval", session.ID),
				)
				os.Exit(utils.ExitCodePendingApproval)
			}
			if utils.OutputFormat == utils.OutputFormatJSON {
				printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, createSuccessMessage(session), session, nil))
			}
			return
		}

		// Phase 2: poll. With --use we wait for active; otherwise approved is enough.
		finalSession, err := pollForApproval(ac, session.ID, useAfterCreate)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opCreate, err, "%s", err)
		}

		if !useAfterCreate {
			message := fmt.Sprintf("Work session %s approved.", session.ID)
			if utils.OutputFormat == utils.OutputFormatJSON {
				printWorkSessionMutationJSON(newWorkSessionMutationOutput(opCreate, message, finalSession, nil))
				return
			}
			utils.CliSuccess("%s", message)
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
	},
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
		d, err := time.ParseDuration(expiresIn)
		if err != nil {
			return "", fmt.Errorf("invalid --expires-in value %q: %w", expiresIn, err)
		}
		if d <= 0 {
			return "", fmt.Errorf("invalid --expires-in value %q: must be a positive duration", expiresIn)
		}
		return time.Now().UTC().Add(d).Format(time.RFC3339), nil
	}
	if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		return "", fmt.Errorf("invalid --expires-at value %q: must be RFC3339 format", expiresAt)
	}
	return expiresAt, nil
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
		commands := splitCSV(spec)
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

// splitCSV splits a comma-separated string and trims whitespace.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// pollForApproval polls every 10 seconds until the session reaches a terminal state.
// untilActive=false returns on approved or active; untilActive=true returns only on
// active (continues polling on approved until the server auto-activates).
func pollForApproval(ac *client.AlpaconClient, id string, untilActive bool) (*wsapi.WorkSession, error) {
	for attempt := 1; attempt <= pollMaxAttempts; attempt++ {
		s, err := wsapi.GetWorkSession(ac, id)
		if err != nil {
			return nil, fmt.Errorf("polling failed: %w", err)
		}
		switch s.Status {
		case activeWorkSessionStatus:
			return s, nil
		case approvedWorkSessionStatus:
			if !untilActive {
				return s, nil
			}
		case rejectedWorkSessionStatus:
			return nil, errors.New("work session was rejected")
		case expiredWorkSessionStatus:
			return nil, errors.New("work session expired while waiting for approval")
		case revokedWorkSessionStatus:
			return nil, errors.New("work session was revoked")
		case completedWorkSessionStatus:
			return nil, errors.New("work session was completed unexpectedly")
		}
		waitMsg := waitMsgApproval
		if s.Status == approvedWorkSessionStatus {
			waitMsg = waitMsgActivation
		}
		utils.CliInfo("%s (attempt %d/%d)", waitMsg, attempt, pollMaxAttempts)
		if attempt < pollMaxAttempts {
			time.Sleep(pollInterval)
		}
	}
	return nil, fmt.Errorf("timed out waiting for approval after %d attempts", pollMaxAttempts)
}

func init() {
	workSessionCreateCmd.Flags().StringVar(&purpose, "purpose", "", "Session purpose (required in non-interactive mode)")
	workSessionCreateCmd.Flags().StringSliceVar(&createScopes, "scope", nil, "Scopes to request. Valid: command, editor, sudo, tunnel, webftp, websh (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringSliceVar(&createServers, "server", nil, "Target server names (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringVar(&expiresIn, "expires-in", "", "Session duration (e.g. 1h, 2h, 4h)")
	workSessionCreateCmd.Flags().StringVar(&expiresAt, "expires-at", "", "Absolute expiry time (RFC3339)")
	workSessionCreateCmd.Flags().StringVar(&requesterType, "requester-type", "user", "Requester type: 'user' (default) or 'agent' (set when an AI agent drives the session)")
	workSessionCreateCmd.Flags().BoolVar(&waitApproval, "wait", false, "Poll until the session is approved, then exit (does not set as active; combine with --use to attach automatically)")
	workSessionCreateCmd.Flags().BoolVar(&useAfterCreate, "use", false, "Set the created session as the workspace's active session (requires status to reach 'active'; combine with --wait when approval is needed)")
	workSessionCreateCmd.Flags().StringArrayVar(&createSudo, "sudo", nil, "Pre-declare sudo command patterns to run without interactive MFA (repeatable; each value is a comma-separated pattern list forming one policy, wildcards allowed; literal commas inside a pattern are not supported — pass the flag again for each policy that needs them). Required for non-interactive sudo via 'exec' (e.g. AI agents). Implies the 'sudo' scope. Patterns are submitted for approval with the session.")
	workSessionCreateCmd.Flags().StringVar(&createSudoReason, "sudo-reason", "", "Justification applied to the sudo policies created via --sudo")
	workSessionCreateCmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
}
