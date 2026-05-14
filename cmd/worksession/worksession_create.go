package worksession

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/server"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	purpose       string
	createScopes  []string
	createServers []string
	expiresIn     string
	expiresAt     string
	requesterType string
	waitApproval  bool
)

var workSessionCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new work session",
	Example: `  alpacon work-session create --purpose "nginx fix" --scope command,websh --server web-01 --expires-in 2h
  alpacon work-session create --purpose "deploy" --scope command --server web-01,db-01 --expires-at 2026-05-09T10:00:00Z --wait`,
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

		var scopeList []string
		for _, s := range createScopes {
			if s = strings.TrimSpace(s); s != "" {
				scopeList = append(scopeList, s)
			}
		}
		if len(scopeList) == 0 {
			utils.CliErrorWithExit("--scope must contain at least one valid scope.")
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
		}

		session, err := wsapi.CreateWorkSession(ac, req)
		if err != nil {
			utils.CliErrorWithExit("Failed to create work session: %s.", err)
		}

		utils.CliSuccess("Work session created: %s (status: %s)", session.ID, session.Status)

		if waitApproval {
			if err := pollForApproval(ac, session.ID); err != nil {
				utils.CliErrorWithExit("%s", err)
			}
			utils.CliSuccess("Work session %s approved.", session.ID)
		}
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

// pollForApproval polls every 10 seconds until the session is approved/active,
// a terminal status is reached (rejected/expired/revoked/completed), or attempts are exhausted.
func pollForApproval(ac *client.AlpaconClient, id string) error {
	const maxAttempts = 30
	const interval = 10 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		s, err := wsapi.GetWorkSession(ac, id)
		if err != nil {
			return fmt.Errorf("polling failed: %w", err)
		}
		switch s.Status {
		case "approved", "active":
			return nil
		case "rejected":
			return errors.New("work session was rejected")
		case "expired":
			return errors.New("work session expired while waiting for approval")
		case "revoked":
			return errors.New("work session was revoked")
		case "completed":
			return errors.New("work session was completed unexpectedly")
		}
		utils.CliInfo("Waiting for approval... (attempt %d/%d)", attempt, maxAttempts)
		if attempt < maxAttempts {
			time.Sleep(interval)
		}
	}
	return fmt.Errorf("timed out waiting for approval after %d attempts", maxAttempts)
}

func init() {
	workSessionCreateCmd.Flags().StringVar(&purpose, "purpose", "", "Session purpose")
	workSessionCreateCmd.Flags().StringSliceVar(&createScopes, "scope", nil, "Scopes to request (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringSliceVar(&createServers, "server", nil, "Target server names (repeatable; comma-separated values also accepted)")
	workSessionCreateCmd.Flags().StringVar(&expiresIn, "expires-in", "", "Session duration (e.g. 1h, 2h, 4h)")
	workSessionCreateCmd.Flags().StringVar(&expiresAt, "expires-at", "", "Absolute expiry time (RFC3339)")
	workSessionCreateCmd.Flags().StringVar(&requesterType, "requester-type", "user", "Requester type: user or agent")
	workSessionCreateCmd.Flags().BoolVar(&waitApproval, "wait", false, "Poll until the session is approved, then exit")
	workSessionCreateCmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
}
