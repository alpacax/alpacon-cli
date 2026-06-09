package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	wscmd "github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type activeWorkSessionSummary struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Scopes    []string `json:"scopes"`
	Servers   []string `json:"servers"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

type whoamiOutput struct {
	Username            string                `json:"username,omitempty"`
	Email               string                `json:"email,omitempty"`
	Phone               string                `json:"phone,omitempty"`
	WorkspaceName       string                `json:"workspace_name"`
	WorkspaceURL        string                `json:"workspace_url"`
	AuthMethod          string                `json:"auth_method"`
	AuthClassification  string                `json:"auth_classification"`
	ExpiresAt           string                `json:"expires_at,omitempty"`
	UID                 int                   `json:"uid,omitempty"`
	Shell               string                `json:"shell,omitempty"`
	HomeDirectory       string                `json:"home_directory,omitempty"`
	Role                string                `json:"role,omitempty"`
	Groups              []iam.GroupMembership `json:"groups,omitempty"`
	WorksessionRequired bool                  `json:"work_session_required"`
	// WorksessionRequiredForAccess and ActiveWorksessionCanonical are the
	// machine-readable names. The legacy keys remain for compatibility.
	WorksessionRequiredForAccess bool                      `json:"worksession_required_for_access"`
	ActiveWorksession            *activeWorkSessionSummary `json:"active_work_session"`
	ActiveWorksessionCanonical   *activeWorkSessionSummary `json:"active_worksession"`
}

func (o whoamiOutput) MarshalJSON() ([]byte, error) {
	type wire whoamiOutput
	normalized := o.normalized()
	return json.Marshal(wire(normalized))
}

func (o whoamiOutput) normalized() whoamiOutput {
	if o.AuthClassification == "" {
		o.AuthClassification = authClassificationFromMethod(o.AuthMethod)
	}

	required := o.WorksessionRequired || o.WorksessionRequiredForAccess
	o.WorksessionRequired = required
	o.WorksessionRequiredForAccess = required

	active := o.ActiveWorksession
	if active == nil {
		active = o.ActiveWorksessionCanonical
	}
	o.ActiveWorksession = active
	o.ActiveWorksessionCanonical = active
	return o
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami [flags]",
	Short: "Display current authenticated identity",
	Long: `Display the current authenticated identity, workspace, system user info,
and permissions. Useful for verifying context before running infrastructure
commands, especially for AI agents and operators managing multiple workspaces.`,
	Example: `  alpacon whoami
  alpacon whoami --output json`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' to authenticate.")
		}

		worksessionRequired := isWorksessionRequired(cfg)
		output := whoamiOutput{
			WorkspaceName:                cfg.WorkspaceName,
			WorkspaceURL:                 cfg.WorkspaceURL,
			AuthMethod:                   config.GetAuthMethod(cfg),
			AuthClassification:           getAuthClassification(cfg),
			ExpiresAt:                    getExpiresAt(cfg),
			WorksessionRequired:          worksessionRequired,
			WorksessionRequiredForAccess: worksessionRequired,
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliWarning("Could not create authenticated API client: %s", err)
			utils.CliWarning("Showing local config only. Server fields are unavailable.")
			warnIfExpiringSoon(cfg)
			printWhoami(output)
			return
		}

		user, err := iam.GetCurrentUser(ac)
		if err != nil {
			utils.CliWarning("Could not fetch user info: %s", err)
			warnIfExpiringSoon(cfg)
			printWhoami(output)
			return
		}

		output.Username = user.Username
		output.Email = user.Email
		output.Phone = user.Phone
		output.UID = user.UID
		output.Shell = user.Shell
		output.HomeDirectory = user.HomeDirectory
		output.Role = getRole(user.IsStaff, user.IsSuperuser)

		groups, err := iam.GetUserMemberships(ac, user.ID)
		if err != nil {
			utils.CliWarning("Could not fetch group memberships: %s", err)
		} else {
			output.Groups = groups
		}

		if output.WorksessionRequired {
			_, ws, wsErr := wscmd.RunCurrent(ac)
			if wsErr != nil {
				utils.CliWarning("Could not fetch active work-session: %s", wsErr)
			} else if ws != nil {
				serverNames := make([]string, len(ws.Servers))
				for i, srv := range ws.Servers {
					serverNames[i] = srv.Name
				}
				expiresAt := ""
				if !ws.ExpiresAt.IsZero() {
					expiresAt = ws.ExpiresAt.UTC().Format(time.RFC3339)
				}
				// normalized() mirrors this into ActiveWorksessionCanonical.
				output.ActiveWorksession = &activeWorkSessionSummary{
					ID:        ws.ID,
					Status:    ws.Status,
					Scopes:    ws.Scopes,
					Servers:   serverNames,
					ExpiresAt: expiresAt,
				}
			}
		}

		warnIfExpiringSoon(cfg)
		printWhoami(output)
	},
}

func getExpiresAt(cfg config.Config) string {
	if cfg.Token != "" && cfg.ExpiresAt != "" {
		return cfg.ExpiresAt
	}
	return ""
}

func getRole(isStaff, isSuperuser bool) string {
	if isSuperuser {
		return "superuser"
	}
	if isStaff {
		return "staff"
	}
	return "user"
}

func getAuthClassification(cfg config.Config) string {
	if cfg.AccessToken != "" {
		return "browser_login"
	}
	if cfg.Token != "" {
		return "token"
	}
	return "unknown"
}

func authClassificationFromMethod(method string) string {
	switch method {
	case "Browser login":
		return "browser_login"
	case "Token":
		return "token"
	default:
		return "unknown"
	}
}

func isTokenAuth(cfg config.Config) bool {
	return cfg.Token != "" && cfg.AccessToken == ""
}

func isWorksessionRequired(cfg config.Config) bool {
	return cfg.AccessToken != ""
}

func warnIfExpiringSoon(cfg config.Config) {
	if !isTokenAuth(cfg) {
		return
	}

	expiresAt := getExpiresAt(cfg)
	if expiresAt == "" {
		return
	}

	expireTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return
	}

	remaining := time.Until(expireTime)
	if remaining < 0 {
		utils.CliWarning("Token has expired. Run 'alpacon login' to re-authenticate.")
	} else if remaining < time.Hour {
		utils.CliWarning("Token expires in %s. Consider re-authenticating soon.", formatDuration(remaining))
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func formatExpiresHuman(expiresAt string) string {
	if expiresAt == "" {
		return ""
	}

	expireTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return expiresAt
	}

	remaining := time.Until(expireTime)
	local := expireTime.Local().Format("2006-01-02 15:04 MST")

	if remaining < 0 {
		return fmt.Sprintf("%s (expired)", local)
	}

	days := int(remaining.Hours() / 24)
	hours := int(remaining.Hours()) % 24

	if days > 0 {
		return fmt.Sprintf("%s (%dd %dh remaining)", local, days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%s (%dh %dm remaining)", local, hours, int(remaining.Minutes())%60)
	}
	return fmt.Sprintf("%s (%dm remaining)", local, int(remaining.Minutes()))
}

func formatGroups(groups []iam.GroupMembership) string {
	if len(groups) == 0 {
		return ""
	}

	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = fmt.Sprintf("%s (%s)", g.Name, g.Role)
	}
	return strings.Join(parts, ", ")
}

func formatWSRequired(required bool, active *activeWorkSessionSummary) string {
	if !required {
		return "no"
	}
	if active == nil {
		return "yes—run 'alpacon work-session list' to see available sessions"
	}
	return fmt.Sprintf("yes—active session %s (%s)", active.ID, active.Status)
}

func printWhoami(output whoamiOutput) {
	if utils.OutputFormat == utils.OutputFormatJSON {
		// MarshalJSON normalizes, so no need to pre-normalize here.
		body, err := json.Marshal(output)
		if err != nil {
			utils.CliErrorWithExit("Failed to marshal whoami: %s", err)
		}
		utils.PrintJson(body)
		return
	}

	output = output.normalized()
	lines := []struct {
		label string
		value string
	}{
		{"User", output.Username},
		{"Email", output.Email},
		{"Phone", output.Phone},
		{"Workspace", fmt.Sprintf("%s (%s)", output.WorkspaceName, output.WorkspaceURL)},
		{"Auth", output.AuthMethod},
		{"Auth class", output.AuthClassification},
		{"Expires", formatExpiresHuman(output.ExpiresAt)},
		{"UID", fmt.Sprintf("%d", output.UID)},
		{"Shell", output.Shell},
		{"Home", output.HomeDirectory},
		{"Role", output.Role},
		{"Groups", formatGroups(output.Groups)},
		{"WS required", formatWSRequired(output.WorksessionRequiredForAccess, output.ActiveWorksessionCanonical)},
	}

	for _, l := range lines {
		if l.value == "" || l.value == "0" {
			continue
		}
		fmt.Fprintf(os.Stdout, "%-13s%s\n", l.label+":", l.value)
	}
}
