package worksession

import (
	"errors"
	"fmt"
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

Pass --use to set the new session as the workspace's active session, so subsequent
exec/websh/cp/tunnel commands attach to it without --work-session. When approval is
required, combine --use with --wait. The session is attached once it reaches the
active state.

If the work needs sudo, pre-declare the command patterns with --sudo. This attaches
MFA-bypass sudo policies to the session so a non-interactive caller (e.g. an AI agent
running 'exec') can run those exact sudo commands without an interactive MFA
prompt. The 'sudo' scope is added automatically, and the policies are submitted for
approval together with the session. If a sudo command is later denied, add it to the
session with 'alpacon work-session update <id> --sudo "<command>"'.`,
	Example: `  alpacon work-session create --purpose "nginx fix" --scope command,websh --server web-01 --expires-in 2h
  alpacon work-session create --purpose "deploy" --scope command --server web-01,db-01 --expires-at 2027-01-15T10:00:00Z --wait
  alpacon work-session create --purpose "hotfix" --scope command --server web-01 --expires-in 1h --use
  alpacon work-session create --purpose "deploy" --scope command --server web-01 --expires-in 2h --wait --use
  alpacon work-session create --purpose "nginx hotfix" --server web-01 --expires-in 2h \
    --sudo "systemctl restart nginx,systemctl reload nginx" --sudo "tail -f /var/log/nginx/*.log"`,
	Run: func(cmd *cobra.Command, args []string) {
		purpose = strings.TrimSpace(purpose)
		if purpose == "" {
			if !utils.IsInteractiveShell() {
				utils.CliErrorWithExit("Non-interactive mode requires --purpose.")
			}
			purpose = utils.PromptForRequiredInput("Purpose: ")
		}
		if len(createScopes) == 0 {
			if !utils.IsInteractiveShell() {
				utils.CliErrorWithExit("Non-interactive mode requires --scope.")
			}
			createScopes = splitCSV(utils.PromptForRequiredInput("Scopes (comma-separated, e.g. command,websh): "))
		}
		if len(createServers) == 0 {
			if !utils.IsInteractiveShell() {
				utils.CliErrorWithExit("Non-interactive mode requires --server.")
			}
			createServers = splitCSV(utils.PromptForRequiredInput("Servers (comma-separated server names): "))
		}

		expiresAtVal, err := parseExpiryFlag(expiresIn, expiresAt)
		if err != nil {
			if expiresIn == "" && expiresAt == "" {
				if !utils.IsInteractiveShell() {
					utils.CliErrorWithExit("Non-interactive mode requires --expires-in or --expires-at.")
				}
				expiresIn = utils.PromptForRequiredInput("Expires in (e.g. 1h, 2h, 4h): ")
				expiresAtVal, err = parseExpiryFlag(expiresIn, "")
				if err != nil {
					utils.CliErrorWithExit("Invalid expiry: %s.", err)
				}
			} else {
				utils.CliErrorWithExit("Invalid expiry: %s.", err)
			}
		}

		if requesterType != "user" && requesterType != "agent" {
			utils.CliErrorWithExit("Invalid --requester-type %q: must be \"user\" or \"agent\".", requesterType)
		}

		// Pre-validate --use to avoid creating an orphan server-side session that we
		// can't attach to the workspace.
		if useAfterCreate {
			if requesterType == "agent" {
				utils.CliErrorWithExit("--use cannot be combined with --requester-type=agent (agent sessions are not workspace-attachable).")
			}
			cfg, err := config.LoadConfig()
			if err != nil || cfg.WorkspaceName == "" {
				utils.CliErrorWithExit("--use requires an active workspace. Run 'alpacon login' first.")
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
			utils.CliErrorWithExit("--scope must contain at least one valid scope.")
		}
		if err := validateScopeEnum(scopeList); err != nil {
			utils.CliErrorWithExit("Invalid --scope: %s", err)
		}
		if err := validateAgentScopes(requesterType, scopeList); err != nil {
			utils.CliErrorWithExit("Invalid --scope: %s", err)
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		var serverNames []string
		for _, s := range createServers {
			if s = strings.TrimSpace(s); s != "" {
				serverNames = append(serverNames, s)
			}
		}
		if len(serverNames) == 0 {
			utils.CliErrorWithExit("--server must contain at least one valid server name.")
		}
		serverIDs := make([]string, 0, len(serverNames))
		for _, name := range serverNames {
			id, err := server.GetServerIDByName(ac, name)
			if err != nil {
				utils.CliErrorWithExit("Server %q not found: %s.", name, err)
			}
			serverIDs = append(serverIDs, id)
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
			utils.CliErrorWithExit("Failed to create work session: %s.", err)
		}

		utils.CliSuccess("Work session created: %s (status: %s)", session.ID, session.Status)

		// Phase 1: post-create decision. Cases without an explicit return delegate to
		// the --wait branch below (or exit immediately when --wait is not set).
		switch decideUseAction(session.Status, useAfterCreate) {
		case useDecisionUseNow:
			attachActiveOrExit(ac, session.ID, "Work session created (%s) but failed to set as active: %s. Run 'alpacon work-session use %s' to retry.", "")
			return
		case useDecisionSkipScheduled:
			if !waitApproval {
				utils.CliInfo("Session is scheduled to activate. Run 'alpacon work-session use %s' once active.", session.ID)
				return
			}
		case useDecisionErrorNeedsWait:
			if !waitApproval {
				utils.CliErrorWithExit("--use requires the session to be active. Pass --wait to wait for approval, or run 'alpacon work-session use %s' after approval.", session.ID)
			}
		}

		if !waitApproval {
			return
		}

		// Phase 2: poll. With --use we wait for active; otherwise approved is enough.
		if err := pollForApproval(ac, session.ID, useAfterCreate); err != nil {
			utils.CliErrorWithExit("%s", err)
		}

		if !useAfterCreate {
			utils.CliSuccess("Work session %s approved.", session.ID)
			return
		}

		// Phase 3: --wait --use. pollForApproval(untilActive=true) guarantees status reached active.
		attachActiveOrExit(ac, session.ID, "Work session %s approved but failed to set as active: %s. Run 'alpacon work-session use %s' to retry.", fmt.Sprintf("Work session %s approved. ", session.ID))
	},
}

type useDecision int

const (
	useDecisionNoop useDecision = iota
	useDecisionUseNow
	useDecisionErrorNeedsWait
	useDecisionSkipScheduled
)

// attachActiveOrExit calls RunUse and prints either a success line (with description if present)
// or exits with the supplied error format (which receives session ID, error, session ID in order).
// successPrefix is prepended to the success line so callers can distinguish phases.
func attachActiveOrExit(ac *client.AlpaconClient, id, errFmt, successPrefix string) {
	desc, err := RunUse(ac, id)
	if err != nil {
		utils.CliErrorWithExit(errFmt, id, err, id)
	}
	suffix := ""
	if desc != "" {
		suffix = fmt.Sprintf(" (%s)", desc)
	}
	utils.CliSuccess("%sActive work-session set to %s%s.", successPrefix, id, suffix)
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
func pollForApproval(ac *client.AlpaconClient, id string, untilActive bool) error {
	for attempt := 1; attempt <= pollMaxAttempts; attempt++ {
		s, err := wsapi.GetWorkSession(ac, id)
		if err != nil {
			return fmt.Errorf("polling failed: %w", err)
		}
		switch s.Status {
		case activeWorkSessionStatus:
			return nil
		case approvedWorkSessionStatus:
			if !untilActive {
				return nil
			}
		case rejectedWorkSessionStatus:
			return errors.New("work session was rejected")
		case expiredWorkSessionStatus:
			return errors.New("work session expired while waiting for approval")
		case revokedWorkSessionStatus:
			return errors.New("work session was revoked")
		case completedWorkSessionStatus:
			return errors.New("work session was completed unexpectedly")
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
	return fmt.Errorf("timed out waiting for approval after %d attempts", pollMaxAttempts)
}

func init() {
	workSessionCreateCmd.Flags().StringVar(&purpose, "purpose", "", "Session purpose")
	workSessionCreateCmd.Flags().StringSliceVar(&createScopes, "scope", nil, "Scopes to request. Valid: command, editor, sudo, tunnel, webftp, websh (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringSliceVar(&createServers, "server", nil, "Target server names (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringVar(&expiresIn, "expires-in", "", "Session duration (e.g. 1h, 2h, 4h)")
	workSessionCreateCmd.Flags().StringVar(&expiresAt, "expires-at", "", "Absolute expiry time (RFC3339)")
	workSessionCreateCmd.Flags().StringVar(&requesterType, "requester-type", "user", "Requester type: user or agent")
	workSessionCreateCmd.Flags().BoolVar(&waitApproval, "wait", false, "Poll until the session is approved, then exit (does not set as active; combine with --use to attach automatically)")
	workSessionCreateCmd.Flags().BoolVar(&useAfterCreate, "use", false, "Set the created session as the workspace's active session (requires status to reach 'active'; combine with --wait when approval is needed)")
	workSessionCreateCmd.Flags().StringArrayVar(&createSudo, "sudo", nil, "Pre-declare sudo command patterns to run without interactive MFA (repeatable; each value is a comma-separated pattern list forming one policy, wildcards allowed; literal commas inside a pattern are not supported — pass the flag again for each policy that needs them). Required for non-interactive sudo via 'exec' (e.g. AI agents). Implies the 'sudo' scope. Patterns are submitted for approval with the session.")
	workSessionCreateCmd.Flags().StringVar(&createSudoReason, "sudo-reason", "", "Justification applied to the sudo policies created via --sudo")
	workSessionCreateCmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
}
